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
	cloud "github.com/mattermost/mattermost-cloud/model"
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

	payload, err := cloud.WebhookPayloadFromReader(strings.NewReader(request.Body))
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

func processWebhookEvent(payload *cloud.WebhookPayload) {
	str, err := payload.ToJSON()
	if err != nil {
		log.Errorf("Failed to marshal fields to JSON, %v", err)
		return
	}
	log.Debug(str)

	switch payload.Type {
	case cloud.TypeCluster:
		err = handleClusterWebhook(payload)
		if err != nil {
			log.Errorf("Failed to handle the cluster webhook, %v", err)
		}
	case cloud.TypeInstallation:
		err = handleInstallationWebhook(payload)
		if err != nil {
			log.Errorf("Failed to handle the installation webhook, %v", err)
		}
	default:
		return
	}
}

func handleClusterWebhook(payload *cloud.WebhookPayload) error {
	provisionerEnv := strings.ToUpper(payload.ExtraData["Environment"])
	if provisionerEnv == "" {
		return errors.New("missing environment from payload")
	}

	mmWebhook := os.Getenv(fmt.Sprintf("MATTERMOST_WEBHOOK_%s", provisionerEnv))
	if mmWebhook == "" {
		return errors.New("missing Mattermost Webhook variable")
	}

	mmWebhookAlert := os.Getenv(fmt.Sprintf("MATTERMOST_WEBHOOK_ALERT_%s", provisionerEnv))
	if mmWebhookAlert == "" {
		return errors.New("missing Mattermost Webhook Alert variable")
	}

	if payload.Type != cloud.TypeCluster {
		return fmt.Errorf("Unable to process payload type %s in 'handleClusterWebhook'", payload.Type)
	}

	attach := mmAttachment{
		Color: "#006400",
	}

	alert := false

	if payload.NewState == cloud.ClusterStateResizeFailed || payload.NewState == cloud.ClusterStateCreationFailed ||
		payload.NewState == cloud.ClusterStateDeletionFailed || payload.NewState == cloud.ClusterStateUpgradeFailed ||
		payload.NewState == cloud.ClusterStateProvisioningFailed {
		attach.Color = "#FF0000"
		alert = true
	}

	attach = *attach.AddField(mmField{Title: "Cluster ID", Value: payload.ID, Short: true})
	attach = *attach.AddField(mmField{Title: "Type", Value: payload.Type.String(), Short: true})
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
		Username:    fmt.Sprintf("Provisioner-%s", provisionerEnv),
		ImageURL:    "https://cdn2.iconfinder.com/data/icons/amazon-aws-stencils/100/Non-Service_Specific_copy__AWS_Cloud-128.png",
		Attachments: []mmAttachment{attach},
	}

	if alert {
		sendMattermostWebhook(mmWebhookAlert, mmPayload)
		sendPagerDutyNotification(payload)
	}

	return sendMattermostWebhook(mmWebhook, mmPayload)
}

func handleInstallationWebhook(payload *cloud.WebhookPayload) error {
	provisionerEnv := strings.ToUpper(payload.ExtraData["Environment"])
	if provisionerEnv == "" {
		return errors.New("missing environment from payload")
	}

	mmWebhook := os.Getenv(fmt.Sprintf("MATTERMOST_WEBHOOK_%s", provisionerEnv))
	if mmWebhook == "" {
		return errors.New("missing Mattermost Webhook variable")
	}

	mmWebhookAlert := os.Getenv(fmt.Sprintf("MATTERMOST_WEBHOOK_ALERT_%s", provisionerEnv))
	if mmWebhookAlert == "" {
		return errors.New("missing Mattermost Webhook Alert variable")
	}

	if payload.Type != cloud.TypeInstallation {
		return fmt.Errorf("Unable to process payload type %s in 'handleInstallationWebhook'", payload.Type)
	}

	attach := mmAttachment{
		Color: "#80B3FA",
	}

	alert := false
	if payload.NewState == cloud.InstallationStateCreationFailed || payload.NewState == cloud.InstallationStateDeletionFailed ||
		payload.NewState == cloud.InstallationStateUpdateFailed || payload.NewState == cloud.InstallationStateCreationNoCompatibleClusters {
		attach.Color = "#FF0000"
		alert = true
	}

	if payload.NewState == cloud.InstallationStateCreationNoCompatibleClusters {
		attach = *attach.AddField(mmField{Title: "**No Compatible Clusters!!**", Short: false})
	}
	attach = *attach.AddField(mmField{Title: "Installation ID", Value: payload.ID, Short: true})
	attach = *attach.AddField(mmField{Title: "Type", Value: payload.Type.String(), Short: true})
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

	title := "Installation Event"
	attach.Title = &title

	mmPayload := mmSlashResponse{
		Username:    fmt.Sprintf("Provisioner-%s", provisionerEnv),
		ImageURL:    "https://cdn2.iconfinder.com/data/icons/amazon-aws-stencils/100/Non-Service_Specific_copy__AWS_Cloud-128.png",
		Attachments: []mmAttachment{attach},
	}

	if alert {
		err := sendMattermostWebhook(mmWebhookAlert, mmPayload)
		if err != nil {
			return err
		}
		err = sendPagerDutyNotification(payload)
		if err != nil {
			return err
		}
		return nil
	}

	if payload.NewState == cloud.InstallationStateCreationRequested {
		return sendMattermostWebhook(mmWebhook, mmPayload)
	}

	if payload.OldState == cloud.InstallationStateCreationInProgress && payload.NewState == cloud.InstallationStateStable {
		return sendMattermostWebhook(mmWebhook, mmPayload)
	}

	return nil
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
	req.Header.Set("X-Custom-Header", "provisioner-webhook-notifier")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func sendPagerDutyNotification(payload *cloud.WebhookPayload) error {
	provisionerEnv := strings.ToUpper(payload.ExtraData["Environment"])
	if provisionerEnv == "" {
		return errors.New("missing environment from payload")
	}

	integrationKey := os.Getenv("PAGERDUTY_INTEGRATION_KEY")
	if integrationKey == "" {
		log.Println("No PagerDuty Integration Key setup")
		return errors.New("missing pagerduty integration key")
	}

	tm := time.Unix(0, payload.Timestamp)
	alertReq := &pagerduty.V2Payload{
		Summary:  fmt.Sprintf("%s - %s %s", payload.Type, payload.ID, payload.NewState),
		Source:   "Alarm System",
		Severity: "critical",
		Details: map[string]string{
			"Type":      payload.Type.String(),
			"State":     payload.NewState,
			"Old_State": payload.OldState,
			"Timestamp": tm.String(),
			"Env":       provisionerEnv,
		},
	}
	for key, value := range payload.ExtraData {
		alertReq.Details[key] = value
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
