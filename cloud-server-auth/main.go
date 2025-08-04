// Package main provides an AWS Lambda function that acts as a proxy, validating and relaying requests
// to cloud server (provisioner). It checks for specific path prefixes and exact path matches to determine if a request
// is authorized. The function also sends notifications to a configured Mattermost webhook in case of
// authentication failures, providing detailed request information and error messages for debugging purposes.
// Additionally, it contains utilities for compiling regex patterns and retrieving environment variables,
// crucial for the operation and configuration of the Lambda function.
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
	"time"

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

// Config holds environment variables for cloud server and Mattermost webhook URLs.
type Config struct {
	CloudServerURL       string
	MattermostWebhookURL string
}

type errorResponse struct {
	Error string `json:"error"`
}

type webhookRequest struct {
	Username string `json:"username"`
	Text     string `json:"text"`
	IconURL  string `json:"icon_url"`
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
	cloudServerURL := os.Getenv(cloudServerEnv)
	if cloudServerURL == "" {
		return nil, fmt.Errorf("environment variable %s is not set", cloudServerEnv)
	}

	mattermostWebhookURL := os.Getenv(mattermostWebhookEnv)
	if mattermostWebhookURL == "" {
		return nil, fmt.Errorf("environment variable %s is not set", mattermostWebhookEnv)
	}

	return &Config{
		CloudServerURL:       cloudServerURL,
		MattermostWebhookURL: mattermostWebhookURL,
	}, nil
}

func validateCloudRequest(config *Config, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	parsedCloudURL, err := url.Parse(config.CloudServerURL)
	if err != nil {
		return processFailedAuth(config, request, http.StatusInternalServerError, errors.Wrapf(err, "cloud server URL %s is invalid", config.CloudServerURL))
	}

	log.Infof("Initial path: %s", request.Path)
	log.Infof("Initial query parameters: %s", request.QueryStringParameters)

	parsedPath, err := url.Parse(request.Path)
	if err != nil {
		return processFailedAuth(config, request, http.StatusBadRequest, err)
	}

	queryValues := make(url.Values)
	for k, v := range request.QueryStringParameters {
		queryValues.Add(k, v)
	}
	parsedPath.RawQuery = queryValues.Encode()

	final := parsedCloudURL.ResolveReference(parsedPath)
	if !isAuthorized(final) {
		return processFailedAuth(config, request, http.StatusUnauthorized, fmt.Errorf("%s is not an authorized path", final.EscapedPath()))
	}

	log.Infof("Final API call: Method %s | %s", request.HTTPMethod, final.String())

	cloudServerRequest, err := http.NewRequest(request.HTTPMethod, final.String(), bytes.NewReader([]byte(request.Body)))
	if err != nil {
		return processFailedAuth(config, request, http.StatusInternalServerError, err)
	}
	cloudServerRequest.Header.Set("Accept-Encoding", "")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(cloudServerRequest)
	if err != nil {
		return processFailedAuth(config, request, http.StatusInternalServerError, errors.Wrap(err, "failed when making request to cloud server"))
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.WithError(closeErr).Error("Failed to close response body")
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return processFailedAuth(config, request, http.StatusInternalServerError, errors.Wrap(err, "failed to read cloud server response body"))
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
		"api/installation", "/api/installation",
		"api/cluster_installation", "/api/cluster_installation",
		"api/webhooks", "/api/webhooks",
		"/api/webhook", "api/webhook",
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(url.EscapedPath(), prefix) {
			return true
		}
	}

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

func processFailedAuth(config *Config, request events.APIGatewayProxyRequest, statusCode int, err error) (events.APIGatewayProxyResponse, error) {
	log.WithError(err).Error("Auth Failure")

	if webhookErr := sendToWebhook(config, request, err); webhookErr != nil {
		log.WithError(webhookErr).Error("Mattermost Webhook Error")
	}

	jsonResponse, _ := json.Marshal(errorResponse{Error: err.Error()})

	return events.APIGatewayProxyResponse{
		StatusCode: statusCode,
		Body:       string(jsonResponse),
	}, err
}

func sendToWebhook(config *Config, request events.APIGatewayProxyRequest, err error) error {
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

	httpRequest, err := http.NewRequest(http.MethodPost, config.MattermostWebhookURL, bytes.NewReader(b))
	if err != nil {
		return err
	}

	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(httpRequest)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("received status code %d", response.StatusCode)
	}

	return nil
}

func main() {
	initLogging()
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	lambda.Start(func(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return validateCloudRequest(config, request)
	})
}
