package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"

	model "github.com/mattermost/mattermost-server/v6/model"
	"github.com/pkg/errors"
)

func send(webhookURL string, payload model.CommandResponse) error {
	marshalContent, _ := json.Marshal(payload)
	var jsonStr = []byte(marshalContent)
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Custom-Header", "aws-sns")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed tο send HTTP request")
	}
	defer resp.Body.Close()

	return nil
}

func sendMattermostNotification(jobName, message string) error {
	attachment := &model.SlackAttachment{
		Color: "#00FF33",
		Fields: []*model.SlackAttachmentField{
			{Title: "New Pipeline to approve", Value: "To abort this job, set the **TO_ABORT** environment variable to `true`", Short: false},
			{Title: jobName, Value: message, Short: false},
		},
	}

	payload := model.CommandResponse{
		Username:    "GitLab Pipeline Manual Approval",
		IconURL:     "https://upload.wikimedia.org/wikipedia/commons/thumb/1/18/GitLab_Logo.svg/1108px-GitLab_Logo.svg.png",
		Attachments: []*model.SlackAttachment{attachment},
	}
	err := send(os.Getenv("MATTERMOST_NOTIFICATION_HOOK"), payload)
	if err != nil {
		return errors.Wrap(err, "failed tο send Mattermost error payload")
	}

	return nil
}
