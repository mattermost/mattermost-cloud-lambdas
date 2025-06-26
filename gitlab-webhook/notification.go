package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

func send(webhookURL string, payload model.CommandResponse) error {
	marshalContent, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "failed to marshal payload")
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(marshalContent))
	if err != nil {
		return errors.Wrap(err, "failed to create HTTP request")
	}
	req.Header.Set("X-Custom-Header", "aws-sns")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send HTTP request")
	}
	defer func() {
		_ = resp.Body.Close() // Explicitly ignore close errors as main operation may have succeeded
	}()

	if resp.StatusCode != http.StatusOK {
		return errors.Wrapf(err, "unexpected response status: %s", resp.Status)
	}

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
