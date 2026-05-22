// Package main provides a Lambda function that prefilters GitHub webhook
// events before forwarding them to a downstream n8n workflow.
//
// The downstream workflow only acts on a small subset of GitHub `pull_request`
// and `pull_request_review` events authored by `cursor[bot]`. GitHub's webhook
// configuration is not granular enough to scope deliveries that tightly, which
// causes the n8n workflow to discard the vast majority of incoming events at a
// non-zero per-execution cost. This Lambda replicates the n8n entry-point
// filters and forwards only events that the workflow would action, preserving
// the original payload, query string, and headers verbatim.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
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
	envN8NWebhookURL    = "N8N_WEBHOOK_URL"
	envWebhookSecret    = "WEBHOOK_SECRET"
	envForwardTimeoutMS = "FORWARD_TIMEOUT_MS"

	defaultForwardTimeout = 10 * time.Second

	cursorBotLogin     = "cursor[bot]"
	mattermostCodeUser = "mattermost-code"
	reviewActionSubmit = "submitted"
	reviewStateApprove = "approved"
)

// Config holds the runtime configuration sourced from environment variables.
type Config struct {
	N8NWebhookURL  string
	WebhookSecret  string
	ForwardTimeout time.Duration
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

func initLogging() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	if os.Getenv("AWS_EXECUTION_ENV") == "" {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
}

func loadConfig() (*Config, error) {
	n8nURL := os.Getenv(envN8NWebhookURL)
	if n8nURL == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envN8NWebhookURL)
	}
	if _, err := url.ParseRequestURI(n8nURL); err != nil {
		return nil, errors.Wrapf(err, "%s is not a valid URL", envN8NWebhookURL)
	}

	secret := os.Getenv(envWebhookSecret)
	if secret == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envWebhookSecret)
	}

	timeout := defaultForwardTimeout
	if raw := os.Getenv(envForwardTimeoutMS); raw != "" {
		parsed, err := time.ParseDuration(raw + "ms")
		if err != nil {
			return nil, errors.Wrapf(err, "%s must be an integer number of milliseconds", envForwardTimeoutMS)
		}
		timeout = parsed
	}

	return &Config{
		N8NWebhookURL:  n8nURL,
		WebhookSecret:  secret,
		ForwardTimeout: timeout,
	}, nil
}

func handler(ctx context.Context, config *Config, client *http.Client, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	if !secretMatches(request, config.WebhookSecret) {
		log.WithField("delivery_id", request.Headers["X-GitHub-Delivery"]).
			Warn("Rejecting webhook: secret mismatch")
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "invalid secret"}), nil
	}

	if request.Body == "" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "empty request body"}), nil
	}

	logger := log.WithFields(log.Fields{
		"delivery_id":  request.Headers["X-GitHub-Delivery"],
		"github_event": request.Headers["X-GitHub-Event"],
	})

	var payload gitHubWebhookPayload
	if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
		logger.WithError(err).Warn("Rejecting webhook: malformed JSON body")
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"}), nil
	}

	if !shouldForward(&payload) {
		logger.Debug("Filtered webhook: does not match downstream workflow conditions")
		return jsonResponse(http.StatusOK, map[string]string{"status": "filtered"}), nil
	}

	logger.Info("Forwarding webhook to n8n")
	if err := forward(ctx, config, client, request); err != nil {
		logger.WithError(err).Error("Failed to forward webhook to n8n")
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "failed to forward webhook"}), nil
	}

	return jsonResponse(http.StatusOK, map[string]string{"status": "forwarded"}), nil
}

// secretMatches compares the `secret` query parameter against the configured
// secret using a constant-time comparison.
func secretMatches(request events.APIGatewayProxyRequest, expected string) bool {
	got := request.QueryStringParameters["secret"]
	if got == "" {
		// Fall back to multi-value parameters when API Gateway uses them.
		if vs := request.MultiValueQueryStringParameters["secret"]; len(vs) > 0 {
			got = vs[0]
		}
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
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

// forward replays the inbound request to n8n with the original body, query
// string, and GitHub-relevant headers preserved verbatim.
func forward(ctx context.Context, config *Config, client *http.Client, request events.APIGatewayProxyRequest) error {
	target, err := url.Parse(config.N8NWebhookURL)
	if err != nil {
		return errors.Wrap(err, "parse n8n webhook URL")
	}

	q := target.Query()
	for k, v := range request.QueryStringParameters {
		q.Set(k, v)
	}
	for k, vs := range request.MultiValueQueryStringParameters {
		q.Del(k)
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	target.RawQuery = q.Encode()

	method := request.HTTPMethod
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader([]byte(request.Body)))
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return errors.Errorf("n8n returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
		"via":
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
