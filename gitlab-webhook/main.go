// Package main provides a Lambda function that handles GitLab webhook events,
// specifically "Pipeline Hook" events, and sends notifications to Mattermost
// based on the event's status.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func main() {
	lambda.Start(handler)
}

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)
}

func handler(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	if request.Body == "" {
		return sendErrorResponse(errors.New("request is empty"))
	}

	eventType := request.Headers["X-Gitlab-Event"]
	if eventType == "" {
		log.Debug(request.Headers)
		return sendErrorResponse(errors.New("no GitLab Event headers"))
	}

	switch eventType {
	case "Pipeline Hook":
		webhookData := PipelineEvent{}
		err := json.NewDecoder(strings.NewReader(request.Body)).Decode(&webhookData)
		if err != nil {
			log.Error(err.Error())
			return sendErrorResponse(err)
		}
		log.Debug(webhookData)

		handlePipelineEvent(webhookData)
	default:
		return sendErrorResponse(errors.Errorf("event %s not implemented", eventType))
	}

	return events.APIGatewayProxyResponse{
		Body:       "{\"status\": \"ok\"}",
		StatusCode: 200,
	}, nil
}

func handlePipelineEvent(webhookData PipelineEvent) {
	log.Info("GitLab Webhook received...")
	for _, build := range webhookData.Builds {
		if build.Status == "manual" && build.Manual {
			if err := sendMattermostNotification(build.Name, fmt.Sprintf("Approve here: %s/-/jobs/%d", webhookData.Project.WebURL, build.ID)); err != nil {
				log.WithError(err).Error("Failed to send Mattermost notification")
			}
			return
		}
	}
}

func sendErrorResponse(err error) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       fmt.Sprintf("{\"error\": \"%s\"}", err.Error()),
		StatusCode: http.StatusBadRequest,
	}, nil
}
