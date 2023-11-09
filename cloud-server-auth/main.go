package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

const (
	cloudServerEnv           = "CLOUD_SERVER"
	mattermostWebhookEnv     = "MATTERMOST_WEBHOOK"
	mattermostWebhookIconURL = "https://images2.minutemediacdn.com/image/upload/c_fill,g_auto,h_1248,w_2220/f_auto,q_auto,w_1100/v1555925520/shape/mentalfloss/800px-princesslineup.jpg"
)

var (
	cloudServerURL       string
	mattermostWebhookURL string
	validPrefixes        = []string{
		"api/installation",
		"/api/installation",
		"api/cluster_installation",
		"/api/cluster_installation",
		"api/webhooks",
		"/api/webhooks",
		"/api/webhook",
		"api/webhook",
	}
	exactMatchRegexes = compileRegexes([]string{
		`^/api/security/installation/[a-zA-Z0-9]{26}/deletion/lock$`,
		`^/api/security/installation/[a-zA-Z0-9]{26}/deletion/unlock$`,
	})
)

type errorResponse struct {
	Error string `json:"error"`
}

type webhookRequest struct {
	Username string `json:"username"`
	Text     string `json:"text"`
	IconURL  string `json:"icon_url"`
}

func init() {
	cloudServerURL = getEnv(cloudServerEnv)
	mattermostWebhookURL = getEnv(mattermostWebhookEnv)

	// Configure logging
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	if os.Getenv("AWS_EXECUTION_ENV") == "" {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
}

func validateCloudRequest(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	cloudServerURL := os.Getenv(cloudServerEnv)
	if cloudServerURL == "" {
		return processFailedAuth(request, http.StatusInternalServerError, fmt.Errorf("cloud server URL var %s not set", cloudServerEnv))
	}
	parsedCloudURL, err := url.Parse(cloudServerURL)
	if err != nil {
		return processFailedAuth(request, http.StatusInternalServerError, errors.Wrapf(err, "cloud server URL %s is invalid", cloudServerURL))
	}

	log.Infof("Initial path: %s", request.Path)
	log.Infof("Initial query parameters: %s", request.QueryStringParameters)

	parsedPath, err := url.Parse(request.Path)
	if err != nil {
		return processFailedAuth(request, http.StatusBadRequest, err)
	}

	queryValues := make(url.Values)
	for k, v := range request.QueryStringParameters {
		queryValues.Add(k, v)
	}
	parsedPath.RawQuery = queryValues.Encode()

	final := parsedCloudURL.ResolveReference(parsedPath)
	if !isAuthorized(final) {
		return processFailedAuth(request, http.StatusUnauthorized, fmt.Errorf("%s is not an authorized path", final.EscapedPath()))
	}

	log.Infof("Final API call: Method %s | %s", request.HTTPMethod, final.String())

	cloudServerRequest, err := http.NewRequest(request.HTTPMethod, final.String(), bytes.NewReader([]byte(request.Body)))
	if err != nil {
		return processFailedAuth(request, http.StatusInternalServerError, err)
	}
	if request.MultiValueHeaders != nil {
		cloudServerRequest.Header = request.MultiValueHeaders
	}
	cloudServerRequest.Header.Set("Accept-Encoding", "")

	client := &http.Client{}
	resp, err := client.Do(cloudServerRequest)
	if err != nil {
		return processFailedAuth(request, http.StatusInternalServerError, errors.Wrap(err, "failed when making request to cloud server"))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return processFailedAuth(request, http.StatusInternalServerError, errors.Wrap(err, "failed to read cloud server response body"))
	}

	log.Info("Success!")

	return events.APIGatewayProxyResponse{
		StatusCode: resp.StatusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}, nil
}

func isAuthorized(url *url.URL) bool {
	validPrefixes := []string{
		"api/installation",
		"/api/installation",
		"api/cluster_installation",
		"/api/cluster_installation",
		"api/webhooks",
		"/api/webhooks",
		"/api/webhook",
		"api/webhook",
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(url.EscapedPath(), prefix) {
			return true
		}
	}

	// These endpoints require an exact match, so the cloud plugin can talk to some but not all security endpoints.
	exactMatchRegexes := []string{
		"^/api/security/installation/[a-zA-Z0-9]{26}/deletion/lock$",
		"^/api/security/installation/[a-zA-Z0-9]{26}/deletion/unlock$",
	}

	for _, regex := range exactMatchRegexes {
		if matched, _ := regexp.MatchString(regex, url.EscapedPath()); matched {
			return true
		}
	}

	return false
}

func processFailedAuth(request events.APIGatewayProxyRequest, statusCode int, err error) (events.APIGatewayProxyResponse, error) {
	log.WithError(err).Error("Auth Failure")

	webhookErr := sendToWebhook(request, err)
	if webhookErr != nil {
		log.WithError(webhookErr).Error("Mattermost Webhook Error")
	}

	json, _ := json.Marshal(errorResponse{Error: err.Error()})

	return events.APIGatewayProxyResponse{
		StatusCode: statusCode,
		Body:       string(json),
	}, err
}

func sendToWebhook(request events.APIGatewayProxyRequest, err error) error {
	if os.Getenv(mattermostWebhookEnv) == "" {
		return fmt.Errorf("mattermost webhook URL var %s not set", mattermostWebhookEnv)
	}

	fullMessage := fmt.Sprintf("Cloud Auth Failure\n---\nError: %s\nMethod: %s\nPath: %s\nRequest ID: %s\n",
		err,
		request.HTTPMethod,
		request.Path,
		request.RequestContext.RequestID,
	)
	if request.Body != "" {
		fullMessage += fmt.Sprintf("```\n%s\n```", request.Body)
	}

	payload := &webhookRequest{
		Username: "Cloud Auth",
		IconURL:  mattermostWebhookIconURL,
		Text:     fullMessage,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpRequest, err := http.NewRequest(http.MethodPost, os.Getenv(mattermostWebhookEnv), bytes.NewReader(b))
	if err != nil {
		return err
	}

	client := http.Client{}
	response, err := client.Do(httpRequest)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("recieved status code %d", response.StatusCode)
	}

	return nil
}

func main() {
	lambda.Start(validateCloudRequest)
}

func compileRegexes(expressions []string) []*regexp.Regexp {
	var regexes []*regexp.Regexp
	for _, expr := range expressions {
		r, err := regexp.Compile(expr)
		if err != nil {
			log.Fatalf("Failed to compile regex: %v", err)
		}
		regexes = append(regexes, r)
	}
	return regexes
}

func getEnv(env string) string {
	value := os.Getenv(env)
	if value == "" {
		log.Fatalf("Environment variable %s is not set", env)
	}
	return value
}
