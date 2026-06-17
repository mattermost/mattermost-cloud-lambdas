// Package main provides a Lambda function that prefilters GitHub webhook
// events before forwarding them to an n8n workflow. See main.go for details.
package main

// gitHubWebhookPayload is a partial representation of the GitHub webhook
// payload covering only the fields required to mirror the n8n filter logic.
type gitHubWebhookPayload struct {
	Action      string       `json:"action"`
	PullRequest *pullRequest `json:"pull_request,omitempty"`
	Review      *review      `json:"review,omitempty"`
}

type pullRequest struct {
	Merged bool         `json:"merged"`
	User   *gitHubActor `json:"user,omitempty"`
}

type review struct {
	State string       `json:"state"`
	User  *gitHubActor `json:"user,omitempty"`
}

type gitHubActor struct {
	Login string `json:"login"`
}
