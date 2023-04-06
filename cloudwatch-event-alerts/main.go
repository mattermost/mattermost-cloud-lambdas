package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/opsgenie/opsgenie-go-sdk-v2/alert"
	"github.com/opsgenie/opsgenie-go-sdk-v2/client"
)

type SNSMessage struct {
	Type      string    `json:"detail-type"`
	Account   string    `json:"account"`
	Resources []string  `json:"resources"`
	Detail    DetailStr `json:"detail"`
}

type DetailStr struct {
	Event      string `json:"event"`
	Result     string `json:"result"`
	Cause      string `json:"cause"`
	SnapshotID string `json:"snapshot_id"`
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, snsEvent events.SNSEvent) {
	log.Info(snsEvent)
	for _, record := range snsEvent.Records {
		snsRecord := record.SNS
		var snsMessage SNSMessage
		if err := json.Unmarshal([]byte(snsRecord.Message), &snsMessage); err != nil {
			log.WithError(err).Error("Decode Error on message notification")
			return
		}

		sendMattermostNotification(record.EventSource, "#FF0000", snsMessage)

		// Trigger OpsGenie
		if os.Getenv("ENVIRONMENT") != "" && os.Getenv("ENVIRONMENT") != "test" {
			sendOpsGenieNotification(snsMessage)
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
		IconUrl:     "https://cdn2.iconfinder.com/data/icons/amazon-aws-stencils/100/Non-Service_Specific_copy__AWS_Cloud-128.png",
		Attachments: attachment,
	}
	if os.Getenv("MATTERMOST_HOOK") != "" {
		send(os.Getenv("MATTERMOST_HOOK"), payload)
	}
}

func sendOpsGenieNotification(snsMessage SNSMessage) {
	if os.Getenv("OPSGENIE_APIKEY") == "" || os.Getenv("OPSGENIE_SCHEDULER_TEAM") == "" {
		log.Warn("No OpsGenie APIKEY/Scheduler team setup")
		return
	}

	alertClient, err := alert.NewClient(&client.Config{
		ApiKey: os.Getenv("OPSGENIE_APIKEY"),
	})
	if err != nil {
		log.WithError(err).Error("not able to create a new opsgenie client")
		return
	}

	detail, _ := json.Marshal(snsMessage.Detail)

	_, err = alertClient.Create(nil, &alert.CreateAlertRequest{
		Message:     "New Cloudwatch Event alert was generated",
		Description: string(detail),
		Responders: []alert.Responder{
			{Type: alert.ScheduleResponder, Name: os.Getenv("OPSGENIE_SCHEDULER_TEAM")},
		},
		Tags: []string{"AWS", "Cloudwatch"},
		Details: map[string]string{
			"Type":      snsMessage.Type,
			"Account":   snsMessage.Account,
			"Resources": strings.Join(snsMessage.Resources, ","),
			"Detail":    string(detail),
		},
		Priority: alert.P1,
	})

}
