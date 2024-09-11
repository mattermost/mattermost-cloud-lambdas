// Copyright (c) 2020 Mattermost, Inc. All Rights Reserved.
// See License.txt for customer information.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	pagerduty "github.com/PagerDuty/go-pagerduty"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	elrond "github.com/mattermost/elrond/model"

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

	payload, err := elrond.WebhookPayloadFromReader(strings.NewReader(request.Body))
	if err != nil {
		return sendErrorResponse(errors.Wrap(err, "failed to parse the body"))
	}
	log.Debug(payload)

	processWebhookEvent(payload)

	return events.APIGatewayProxyResponse{
		Body:       "{\"status\": \"ok\"}",
		StatusCode: 200,
	}, nil

}

func processWebhookEvent(payload *elrond.WebhookPayload) {
	str, err := payload.ToJSON()
	if err != nil {
		log.Errorf("Failed to marshal fields to JSON, %v", err)
		return
	}
	log.Debug(str)

	switch payload.Type {
	case elrond.TypeRing:
		err = handleRingWebhook(payload)
		if err != nil {
			log.Errorf("Failed to handle the cluster webhook, %v", err)
		}
	default:
		return
	}
}

func handleRingWebhook(payload *elrond.WebhookPayload) error {
	elrondEnv := os.Getenv("ENVIRONMENT")
	if elrondEnv == "" {
		return errors.New("missing environment from payload")
	}

	mmWebhook := os.Getenv(fmt.Sprintf("MATTERMOST_ELROND_WEBHOOK_%s", elrondEnv))
	if mmWebhook == "" {
		return errors.New("missing Mattermost Webhook variable")
	}

	mmWebhookAlert := os.Getenv(fmt.Sprintf("MATTERMOST_WEBHOOK_ALERT_%s", elrondEnv))
	if mmWebhookAlert == "" {
		return errors.New("missing Mattermost Webhook Alert variable")
	}

	attach := mmAttachment{
		Color: "#006400",
	}

	alert := false

	if payload.NewState == elrond.RingStateCreationFailed || payload.NewState == elrond.RingStateDeletionFailed ||
		payload.NewState == elrond.RingStateReleaseRollbackFailed || payload.NewState == elrond.RingStateSoakingFailed ||
		payload.NewState == elrond.RingStateReleaseFailed || payload.NewState == elrond.InstallationGroupReleaseFailed ||
		payload.NewState == elrond.InstallationGroupReleaseSoakingFailed {
		attach.Color = "#FF0000"
		alert = true
	}

	attach = *attach.AddField(mmField{Title: "Ring ID", Value: payload.ID, Short: true})
	attach = *attach.AddField(mmField{Title: "Ring Name", Value: payload.Name, Short: true})
	attach = *attach.AddField(mmField{Title: "Type", Value: payload.Type, Short: true})
	attach = *attach.AddField(mmField{Title: "New State", Value: payload.NewState, Short: true})
	attach = *attach.AddField(mmField{Title: "Old State", Value: payload.OldState, Short: true})

	tm := time.Unix(0, payload.Timestamp)
	attach = *attach.AddField(mmField{Title: "Timestamp", Value: tm.String(), Short: true})

	if len(payload.ExtraData) > 0 {
		var extraData []string
		for key, value := range payload.ExtraData {
			extraData = append(extraData, fmt.Sprintf("%s: %s", key, value))
		}
		attach = *attach.AddField(mmField{Title: "Extra Data", Value: strings.Join(extraData, "\n"), Short: false})
	}

	title := "Cluster Event"
	attach.Title = &title

	mmPayload := mmSlashResponse{
		Username:    fmt.Sprintf("Elrond-%s", elrondEnv),
		ImageURL:    "https://www.looper.com/img/gallery/elronds-backstory-explained/intro-1597335791.jpg",
		Attachments: []mmAttachment{attach},
	}

	if alert {
		sendMattermostWebhook(mmWebhookAlert, mmPayload)
		sendPagerDutyNotification(payload)
	}

	return sendMattermostWebhook(mmWebhook, mmPayload)
}

func sendErrorResponse(err error) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{
		Body:       fmt.Sprintf("{\"error\": \"%s\"}", err.Error()),
		StatusCode: http.StatusBadRequest,
	}, nil
}

type mmField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

type mmAttachment struct {
	Fallback   *string    `json:"fallback"`
	Color      string     `json:"color"`
	PreText    *string    `json:"pretext"`
	AuthorName *string    `json:"author_name"`
	AuthorLink *string    `json:"author_link"`
	AuthorIcon *string    `json:"author_icon"`
	Title      *string    `json:"title"`
	TitleLink  *string    `json:"title_link"`
	Text       *string    `json:"text"`
	ImageURL   *string    `json:"image_url"`
	Fields     []*mmField `json:"fields"`
}

type mmSlashResponse struct {
	ResponseType string         `json:"response_type,omitempty"`
	Username     string         `json:"username,omitempty"`
	ImageURL     string         `json:"icon_url,omitempty"`
	Channel      string         `json:"channel,omitempty"`
	Text         string         `json:"text,omitempty"`
	GotoLocation string         `json:"goto_location,omitempty"`
	Attachments  []mmAttachment `json:"attachments,omitempty"`
}

func (attachment *mmAttachment) AddField(field mmField) *mmAttachment {
	attachment.Fields = append(attachment.Fields, &field)
	return attachment
}

func sendMattermostWebhook(webhookURL string, payload mmSlashResponse) error {
	marshalContent, _ := json.Marshal(payload)
	var jsonStr = []byte(marshalContent)
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Custom-Header", "elrond-webhook-notifier")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func sendPagerDutyNotification(payload *elrond.WebhookPayload) error {
	elrondEnv := os.Getenv("ENVIRONMENT")
	if elrondEnv == "" {
		return errors.New("missing environment from payload")
	}

	integrationKey := os.Getenv("PAGERDUTY_INTEGRATION_KEY")
	if integrationKey == "" {
		log.Warn("No PagerDuty Integration Key setup")
		return errors.New("missing pagerduty integration key")
	}

	tm := time.Unix(0, payload.Timestamp)
	alertReq := &pagerduty.V2Payload{
		Summary:  fmt.Sprintf("%s - %s - %s %s", payload.Type, payload.ID, payload.Name, payload.NewState),
		Source:   "Alarm System",
		Severity: "critical",
		Details: map[string]string{
			"Type":      payload.Type,
			"State":     payload.NewState,
			"Old_State": payload.OldState,
			"Timestamp": tm.String(),
			"Env":       elrondEnv,
		},
	}

	event := pagerduty.V2Event{
		RoutingKey: integrationKey,
		Action:     "trigger",
		Payload:    alertReq,
	}

	// Send the event to PagerDuty
	_, err := pagerduty.ManageEvent(event)
	if err != nil {
		log.WithError(err).Error("Failed to send PagerDuty notification")
		return errors.New("Failed to send PagerDuty notification")
	}

	log.Info("PagerDuty event sent successfully")
	return nil
}
