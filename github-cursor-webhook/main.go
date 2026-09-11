// Package main provides a Lambda function that prefilters GitHub webhook
// events before forwarding them to a downstream n8n workflow.
//
// The downstream workflow only acts on a small subset of GitHub `pull_request`
// and `pull_request_review` events authored by `cursor[bot]`. GitHub's webhook
// configuration is not granular enough to scope deliveries that tightly, which
// causes the n8n workflow to discard the vast majority of incoming events at a
// non-zero per-execution cost. This Lambda replicates the n8n entry-point
// filters and forwards only events that the workflow would action.
//
// Inbound requests are authenticated by verifying the GitHub
// X-Hub-Signature-256 HMAC against the raw body using GITHUB_WEBHOOK_SECRET.
// Outbound forwards carry the original body and GitHub headers verbatim and
// add an X-API-KEY header sourced from N8N_API_KEY for the downstream
// workflow to authorize.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

const (
	envN8NWebhookURL       = "N8N_WEBHOOK_URL"
	envGitHubWebhookSecret = "GITHUB_WEBHOOK_SECRET"
	envN8NAPIKey           = "N8N_API_KEY"
	envForwardTimeoutMS    = "FORWARD_TIMEOUT_MS"
	envLogLevel            = "LOG_LEVEL"

	defaultForwardTimeout = 10 * time.Second

	signatureHeader = "X-Hub-Signature-256"
	signaturePrefix = "sha256="
	outboundAPIKey  = "X-API-KEY"
	deliveryHeader  = "X-GitHub-Delivery"
	eventTypeHeader = "X-GitHub-Event"

	cursorBotLogin     = "cursor[bot]"
	mattermostCodeUser = "mattermost-code"
	reviewActionSubmit = "submitted"
	reviewStateApprove = "approved"
)

// Config holds the runtime configuration sourced from environment variables.
type Config struct {
	N8NWebhookURL       string
	GitHubWebhookSecret string
	N8NAPIKey           string
	ForwardTimeout      time.Duration
}

func main() {
	initLogging()

	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	client := &http.Client{Timeout: config.ForwardTimeout}

	lambda.Start(func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return handler(ctx, config, client, request)
	})
}

// initLogging configures structured logging. The level comes from LOG_LEVEL so
// it can be raised without a code change or redeploy of the binary.
//
// This previously inferred the level from AWS_EXECUTION_ENV being empty, on the
// assumption that variable is unset outside Lambda. It is in fact set for the
// provided.al2 runtime too, so the level always resolved to Info in Lambda and
// every debug line was silently dropped in production - which hid the filtered
// path, the single most common outcome, from anyone trying to confirm that
// deliveries were arriving at all.
func initLogging() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(logLevel(os.Getenv(envLogLevel)))
}

// logLevel resolves a LOG_LEVEL value, falling back to Info when it is empty or
// unparseable. An invalid value must not silence logging entirely, so it is
// reported rather than swallowed.
func logLevel(raw string) log.Level {
	if raw == "" {
		return log.InfoLevel
	}

	level, err := log.ParseLevel(raw)
	if err != nil {
		log.WithField(envLogLevel, raw).Warn("Unrecognised log level, defaulting to info")
		return log.InfoLevel
	}

	return level
}

func loadConfig() (*Config, error) {
	n8nURL := os.Getenv(envN8NWebhookURL)
	if n8nURL == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envN8NWebhookURL)
	}
	parsedURL, err := url.ParseRequestURI(n8nURL)
	if err != nil {
		return nil, errors.Wrapf(err, "%s is not a valid URL", envN8NWebhookURL)
	}
	if parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("%s must be an https URL", envN8NWebhookURL)
	}

	githubSecret := os.Getenv(envGitHubWebhookSecret)
	if githubSecret == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envGitHubWebhookSecret)
	}

	n8nAPIKey := os.Getenv(envN8NAPIKey)
	if n8nAPIKey == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envN8NAPIKey)
	}

	timeout := defaultForwardTimeout
	if raw := os.Getenv(envForwardTimeoutMS); raw != "" {
		parsed, err := time.ParseDuration(raw + "ms")
		if err != nil {
			return nil, errors.Wrapf(err, "%s must be an integer number of milliseconds", envForwardTimeoutMS)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("%s must be greater than 0", envForwardTimeoutMS)
		}
		timeout = parsed
	}

	return &Config{
		N8NWebhookURL:       n8nURL,
		GitHubWebhookSecret: githubSecret,
		N8NAPIKey:           n8nAPIKey,
		ForwardTimeout:      timeout,
	}, nil
}

func handler(ctx context.Context, config *Config, client *http.Client, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	logger := log.WithFields(log.Fields{
		"delivery_id":  lookupHeader(request, deliveryHeader),
		"github_event": lookupHeader(request, eventTypeHeader),
	})

	body, err := decodeBody(request)
	if err != nil {
		logger.WithError(err).Warn("Rejecting webhook: failed to decode body")
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"}), nil
	}

	if !signatureMatches(request, body, config.GitHubWebhookSecret) {
		logger.Warn("Rejecting webhook: signature mismatch")
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "invalid signature"}), nil
	}

	if len(body) == 0 {
		logger.Warn("Rejecting webhook: empty request body")
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "empty request body"}), nil
	}

	var payload gitHubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logger.WithError(err).Warn("Rejecting webhook: malformed JSON body")
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"}), nil
	}

	if !shouldForward(&payload) {
		// Info, not Debug: filtering is the expected outcome for most deliveries,
		// so this is the line that tells an operator the endpoint is reachable and
		// authenticating correctly. At Debug it left a successful delivery
		// indistinguishable from one that never arrived.
		logger.Info("Filtered webhook: does not match downstream workflow conditions")
		return jsonResponse(http.StatusOK, map[string]string{"status": "filtered"}), nil
	}

	logger.Info("Forwarding webhook to n8n")
	if err := forward(ctx, config, client, request, body); err != nil {
		logger.WithError(err).Error("Failed to forward webhook to n8n")
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "failed to forward webhook"}), nil
	}

	return jsonResponse(http.StatusOK, map[string]string{"status": "forwarded"}), nil
}

// decodeBody returns the raw request bytes, decoding base64 if API Gateway
// flagged the payload as binary. The HMAC must be computed over these exact
// bytes, so this must be the single source of truth for the payload.
func decodeBody(request events.APIGatewayProxyRequest) ([]byte, error) {
	if request.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(request.Body)
		if err != nil {
			return nil, errors.Wrap(err, "decode base64 body")
		}
		return decoded, nil
	}
	return []byte(request.Body), nil
}

// signatureMatches verifies the GitHub X-Hub-Signature-256 HMAC against the
// raw body using the configured webhook secret.
func signatureMatches(request events.APIGatewayProxyRequest, body []byte, secret string) bool {
	header := lookupHeader(request, signatureHeader)
	if !strings.HasPrefix(header, signaturePrefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return subtle.ConstantTimeCompare(mac.Sum(nil), got) == 1
}

// lookupHeader returns the first value for name in either the single- or
// multi-value header maps, comparing keys case-insensitively because API
// Gateway preserves whatever casing the client sent.
func lookupHeader(request events.APIGatewayProxyRequest, name string) string {
	for k, v := range request.Headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	for k, vs := range request.MultiValueHeaders {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// shouldForward mirrors the n8n entry-point filter chain (Filter1 + If + Filter)
// to decide whether the downstream workflow would do meaningful work.
func shouldForward(p *gitHubWebhookPayload) bool {
	if p.PullRequest == nil || p.PullRequest.User == nil {
		return false
	}
	if p.PullRequest.User.Login != cursorBotLogin {
		return false
	}

	// "If" TRUE branch: PR was merged → workflow resolves the linked Jira issue.
	if p.PullRequest.Merged {
		return true
	}

	// "If" FALSE branch + "Filter": review submissions that are not approvals
	// and not from the mattermost-code automation user.
	if p.Action != reviewActionSubmit || p.Review == nil || p.Review.User == nil {
		return false
	}
	if p.Review.User.Login == mattermostCodeUser {
		return false
	}
	if p.Review.State == reviewStateApprove {
		return false
	}
	return true
}

// forward replays the inbound request to n8n with the original body and
// GitHub-relevant headers preserved verbatim. The downstream workflow
// authenticates via the X-API-KEY header rather than any inbound query
// string, so query parameters from the API Gateway request are not copied.
func forward(ctx context.Context, config *Config, client *http.Client, request events.APIGatewayProxyRequest, body []byte) error {
	method := request.HTTPMethod
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, config.N8NWebhookURL, bytes.NewReader(body))
	if err != nil {
		return errors.Wrap(err, "build forward request")
	}

	for k, v := range request.Headers {
		if shouldDropHeader(k) {
			continue
		}
		req.Header.Set(k, v)
	}
	for k, vs := range request.MultiValueHeaders {
		if shouldDropHeader(k) {
			continue
		}
		req.Header.Del(k)
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set(outboundAPIKey, config.N8NAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "execute forward request")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.WithError(closeErr).Warn("Failed to close n8n response body")
		}
	}()

	// Drain to allow connection reuse, but cap at 4KB for logs.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return errors.Errorf("n8n returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// shouldDropHeader filters out hop-by-hop and AWS/CloudFront-injected headers
// that should not be replayed to n8n.
func shouldDropHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host",
		"content-length",
		"connection",
		"keep-alive",
		"proxy-authenticate",
		"proxy-authorization",
		"te",
		"trailer",
		"transfer-encoding",
		"upgrade",
		"x-forwarded-for",
		"x-forwarded-proto",
		"x-forwarded-port",
		"x-amzn-trace-id",
		"x-amz-cf-id",
		"cloudfront-forwarded-proto",
		"cloudfront-is-desktop-viewer",
		"cloudfront-is-mobile-viewer",
		"cloudfront-is-smarttv-viewer",
		"cloudfront-is-tablet-viewer",
		"cloudfront-viewer-country",
		"via",
		"x-hub-signature",
		"x-hub-signature-256",
		"x-api-key":
		return true
	}
	return false
}

func jsonResponse(status int, body any) events.APIGatewayProxyResponse {
	encoded, err := json.Marshal(body)
	if err != nil {
		encoded = []byte(`{"error":"failed to encode response"}`)
	}
	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(encoded),
	}
}
