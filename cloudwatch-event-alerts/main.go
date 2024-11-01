// Package main defines an AWS Lambda function that processes SNS events, decodes them into
// structured messages, and forwards alerts to both Mattermost and PagerDuty for notifications.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"

	pagerduty "github.com/PagerDuty/go-pagerduty"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// SNSMessage represents the structure of a message received from AWS SNS.
type SNSMessage struct {
	Type      string    `json:"detail-type"`
	Account   string    `json:"account"`
	Resources []string  `json:"resources"`
	Detail    DetailStr `json:"detail"`
}

// DetailStr encapsulates the details of the SNS message relevant to the event.
type DetailStr struct {
	Event      string `json:"event"`
	Result     string `json:"result"`
	Cause      string `json:"cause"`
	SnapshotID string `json:"snapshot_id"`
}

func main() {
	lambda.Start(handler)
}

func handler(_ context.Context, snsEvent events.SNSEvent) {
	log.Info(snsEvent)
	for _, record := range snsEvent.Records {
		snsRecord := record.SNS
		var snsMessage SNSMessage
		if err := json.Unmarshal([]byte(snsRecord.Message), &snsMessage); err != nil {
			log.WithError(err).Error("Decode Error on message notification")
			return
		}

		sendMattermostNotification(record.EventSource, "#FF0000", snsMessage)

		// Trigger PagerDuty
		if os.Getenv("ENVIRONMENT") != "" && os.Getenv("ENVIRONMENT") != "test" {
			sendPagerDutyNotification(snsMessage)
		}
	}
}

func sendMattermostNotification(source, color string, snsMessage SNSMessage) {
	detail, _ := json.Marshal(snsMessage.Detail)

	attachment := []MMAttachment{}
	attach := MMAttachment{
		Color: color,
	}
	attach = *attach.AddField(MMField{Title: "Cloudwatch Event Alert", Short: false})
	attach = *attach.AddField(MMField{Title: "Type", Value: snsMessage.Type, Short: true})
	attach = *attach.AddField(MMField{Title: "Account", Value: snsMessage.Account, Short: true})
	attach = *attach.AddField(MMField{Title: "Resources", Value: strings.Join(snsMessage.Resources, ","), Short: true})
	attach = *attach.AddField(MMField{Title: "Detail", Value: string(detail), Short: true})

	attachment = append(attachment, attach)

	payload := MMSlashResponse{
		Username:    source,
		IconURL:     "https://cdn2.iconfinder.com/data/icons/amazon-aws-stencils/100/Non-Service_Specific_copy__AWS_Cloud-128.png",
		Attachments: attachment,
	}
	if os.Getenv("MATTERMOST_HOOK") != "" {
		send(os.Getenv("MATTERMOST_HOOK"), payload)
	}
}

func sendPagerDutyNotification(snsMessage SNSMessage) {
	integrationKey := os.Getenv("PAGERDUTY_INTEGRATION_KEY")
	if integrationKey == "" {
		log.Warn("No PagerDuty Integration Key setup")
		return
	}

	detail, _ := json.Marshal(snsMessage.Detail)

	detailString := fmt.Sprintf("AWS Account: %s\nResources: %s\nDetail:\n%s",
		snsMessage.Account,
		strings.Join(snsMessage.Resources, ","),
		string(detail),
	)

	event := pagerduty.V2Event{
		RoutingKey: integrationKey,
		Action:     "trigger",
		Payload: &pagerduty.V2Payload{
			Summary:  "New Cloudwatch Event alert was generated",
			Source:   "Alarm System",
			Severity: "critical",
			Details: map[string]interface{}{
				"Message": detailString,
			},
		},
	}

	// Send the event to PagerDuty with context
	ctx := context.Background()
	_, err := pagerduty.ManageEventWithContext(ctx, event)
	if err != nil {
		log.WithError(err).Error("Failed to send PagerDuty notification")
		return
	}

	log.Info("PagerDuty event sent successfully")
}
