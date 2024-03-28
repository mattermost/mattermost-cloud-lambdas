// Package main defines a Lambda function that processes AWS SNS events, specifically related to AWS alarm notifications.
// The function listens for SNS messages that contain alarm state information and handles two types of events:
// 'Started cross AZ failover' and 'Completed failover'. Depending on the type of event, it sends notifications
// with appropriate color coding to a Mattermost channel. In non-test environments, it also interacts with PagerDuty,
// creating or closing alerts corresponding to the received SNS events. The PagerDuty and Mattermost integrations
// require specific environment variables to be set for API keys and webhook URLs. This package is designed to
// streamline incident management workflows by automating alert notifications and updates through common operational
// communication platforms.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	pagerduty "github.com/PagerDuty/go-pagerduty"
	log "github.com/sirupsen/logrus"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// SNSMessageNotification represents the details of an SNS message related to AWS alarms.
type SNSMessageNotification struct {
	SourceID     string `json:"Source ID"`
	EventMessage string `json:"Event Message"`
}

func main() {
	lambda.Start(handler)
}

func handler(_ context.Context, snsEvent events.SNSEvent) {
	for _, record := range snsEvent.Records {
		snsRecord := record.SNS
		var messageNotification SNSMessageNotification
		if err := json.Unmarshal([]byte(snsRecord.Message), &messageNotification); err != nil {
			log.WithError(err).Error("Decode Error on message notification")
			return
		}

		if strings.HasPrefix(messageNotification.EventMessage, "Started cross AZ failover") {
			sendMattermostNotification(record.EventSource, "#FF0000", messageNotification)

			// Trigger PagerDuty
			if os.Getenv("ENVIRONMENT") != "" && os.Getenv("ENVIRONMENT") != "test" {
				sendPagerDutyNotification(messageNotification)
			}
		} else if strings.HasPrefix(messageNotification.EventMessage, "Completed failover") {
			sendMattermostNotification(record.EventSource, "#006400", messageNotification)

			// Trigger PagerDuty
			if os.Getenv("ENVIRONMENT") != "" && os.Getenv("ENVIRONMENT") != "test" {
				closePagerDutyIncidents(messageNotification)
			}
		}
	}
}

func sendMattermostNotification(source, color string, messageNotification SNSMessageNotification) {
	attachment := []MMAttachment{}
	attach := MMAttachment{
		Color: color,
	}
	attach = *attach.AddField(MMField{Title: "RDS DB Cluster Failover", Short: false})
	attach = *attach.AddField(MMField{Title: "Cluster", Value: messageNotification.SourceID, Short: true})
	attach = *attach.AddField(MMField{Title: "Message", Value: messageNotification.EventMessage, Short: true})

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

func sendPagerDutyNotification(messageNotification SNSMessageNotification) {
	integrationKey := os.Getenv("PAGERDUTY_INTEGRATION_KEY")
	if integrationKey == "" {
		log.Warn("No PagerDuty Integration Key setup")
		return
	}

	event := pagerduty.V2Event{
		RoutingKey: integrationKey,
		Action:     "trigger",
		Payload: &pagerduty.V2Payload{
			Summary:  messageNotification.EventMessage,
			Source:   "Alarm System",
			Severity: "critical",
			Details: map[string]string{
				"Cluster": messageNotification.SourceID,
			},
		},
	}

	// Send the event to PagerDuty
	_, err := pagerduty.ManageEvent(event)
	if err != nil {
		log.WithError(err).Error("Failed to send PagerDuty notification")
		return
	}

	log.Info("PagerDuty event sent successfully")

}

func closePagerDutyIncidents(messageNotification SNSMessageNotification) {
	apiKey := os.Getenv("PAGERDUTY_APIKEY")
	email := os.Getenv("EMAIL_ADDRESS")
	if apiKey == "" {
		log.Warn("No PagerDuty APIKEY setup")
		return
	}

	client := pagerduty.NewClient(apiKey)

	var opts pagerduty.ListIncidentsOptions

	opts.Limit = 25 // Set page size, max is often 100
	opts.Offset = 0 // Start with the first page
	opts.Total = true

	var incidents []pagerduty.Incident

	for {
		// List incidents with current pagination options
		res, err := client.ListIncidents(opts)
		if err != nil {
			log.WithError(err).Errorf("Error retrieving incidents: %v")
			break
		}

		// Process the incidents
		for _, incident := range res.Incidents {
			incidents = append(incidents, incident)
		}

		// Check if we've retrieved all incidents
		if opts.Offset+opts.Limit >= res.Total {
			break // Exit loop if we have retrieved all incidents
		}
		opts.Offset += opts.Limit // Prepare for the next page
	}

	for _, incident := range incidents {
		// Check if incident description or details match your criteria
		// This part is up to you on how you match incidents to your messageNotification
		if incident.Description == messageNotification.EventMessage {
			// Resolve the incident

			_, err := client.ManageIncidentsWithContext(context.TODO(), email, []pagerduty.ManageIncidentsOptions{
				{
					ID:     incident.ID,
					Status: "resolved",
				},
			})
			if err != nil {
				log.WithError(err).Errorf("error resolving the incident %s", incident.ID)
				return
			}
		}
	}
}
