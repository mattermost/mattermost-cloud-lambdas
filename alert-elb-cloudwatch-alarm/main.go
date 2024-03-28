// Package main handles AWS SNS messages and creates alerts based on the message contents.
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

// SNSMessageNotification represents the details of an SNS message related to AWS alarms.
type SNSMessageNotification struct {
	AlarmName        string `json:"AlarmName"`
	AlarmDescription string `json:"AlarmDescription,omitempty"`
	AWSAccountID     string `json:"AWSAccountId"`
	NewStateValue    string `json:"NewStateValue"`
	NewStateReason   string `json:"NewStateReason"`
	StateChangeTime  string `json:"StateChangeTime"`
	Region           string `json:"Region"`
	OldStateValue    string `json:"OldStateValue"`
	Trigger          struct {
		MetricName    string `json:"MetricName"`
		Namespace     string `json:"Namespace"`
		StatisticType string `json:"StatisticType"`
		Statistic     string `json:"Statistic"`
		Unit          string `json:"Unit,omitempty"`
		Dimensions    []struct {
			Value string `json:"value"`
			Name  string `json:"name"`
		} `json:"Dimensions"`
		Period                           int     `json:"Period"`
		EvaluationPeriods                int     `json:"EvaluationPeriods"`
		ComparisonOperator               string  `json:"ComparisonOperator"`
		Threshold                        float32 `json:"Threshold"`
		TreatMissingData                 string  `json:"TreatMissingData"`
		EvaluateLowSampleCountPercentile string  `json:"EvaluateLowSampleCountPercentile"`
	} `json:"Trigger"`
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

		sendMattermostNotification(record.EventSource, messageNotification)

		// Trigger PagerDuty
		if os.Getenv("ENVIRONMENT") != "" && os.Getenv("ENVIRONMENT") != "test" {
			if messageNotification.NewStateValue != "OK" {
				sendPagerDutyNotification(messageNotification)
			} else {
				closePagerDutyIncidents(messageNotification)
			}
		}
	}

}

func sendMattermostNotification(source string, messageNotification SNSMessageNotification) {
	attachment := []MMAttachment{}
	attach := MMAttachment{
		Color: "#FF0000",
	}

	if messageNotification.NewStateValue == "OK" {
		attach.Color = "#006400"
	}

	attach = *attach.AddField(MMField{Title: "AlarmName", Value: messageNotification.AlarmName, Short: true})
	attach = *attach.AddField(MMField{Title: "AlarmDescription", Value: messageNotification.AlarmDescription, Short: true})
	attach = *attach.AddField(MMField{Title: "AWS Account", Value: messageNotification.AWSAccountID, Short: true})
	attach = *attach.AddField(MMField{Title: "Region", Value: messageNotification.Region, Short: true})
	attach = *attach.AddField(MMField{Title: "New State", Value: messageNotification.NewStateValue, Short: true})
	attach = *attach.AddField(MMField{Title: "Old State", Value: messageNotification.OldStateValue, Short: true})
	attach = *attach.AddField(MMField{Title: "New State Reason", Value: messageNotification.NewStateReason, Short: false})
	attach = *attach.AddField(MMField{Title: "MetricName", Value: messageNotification.Trigger.MetricName, Short: true})
	attach = *attach.AddField(MMField{Title: "Namespace", Value: messageNotification.Trigger.Namespace, Short: true})

	var dimensions []string
	for _, dimension := range messageNotification.Trigger.Dimensions {
		dimensions = append(dimensions, fmt.Sprintf("%s: %s", dimension.Name, dimension.Value))
	}
	attach = *attach.AddField(MMField{Title: "Dimensions", Value: strings.Join(dimensions, "\n"), Short: false})

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
		log.Println("No PagerDuty Integration Key setup")
		return
	}

	var dimensions []string
	for _, dimension := range messageNotification.Trigger.Dimensions {
		dimensions = append(dimensions, fmt.Sprintf("%s: %s", dimension.Name, dimension.Value))
	}

	detailString := fmt.Sprintf("AWS Account: %s\nRegion: %s\nState: %s\nMetricName: %s\nNamespace: %s\nDimensions:\n%s",
		messageNotification.AWSAccountID,
		messageNotification.Region,
		messageNotification.NewStateValue,
		messageNotification.Trigger.MetricName,
		messageNotification.Trigger.Namespace,
		strings.Join(dimensions, "\n"),
	)

	event := pagerduty.V2Event{
		RoutingKey: integrationKey,
		Action:     "trigger",
		Payload: &pagerduty.V2Payload{
			Summary:  messageNotification.AlarmName + " - " + messageNotification.AlarmDescription,
			Source:   "Alarm System",
			Severity: "critical",
			Details: map[string]interface{}{
				"Message": detailString,
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
		if incident.Description == fmt.Sprintf("%s - %s", messageNotification.AlarmName, messageNotification.AlarmDescription) {
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
