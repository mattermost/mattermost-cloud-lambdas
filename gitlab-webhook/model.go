package main

import "time"

// PipelineEvent describe the Pipeline event from gitlab
type PipelineEvent struct {
	ObjectKind       string           `json:"object_kind"`
	ObjectAttributes ObjectAttributes `json:"object_attributes"`
	MergeRequest     MergeRequest     `json:"merge_request"`
	User             UserInfo         `json:"user"`
	Project          Project          `json:"project"`
	Commit           Commit           `json:"commit"`
	Builds           []Builds         `json:"builds"`
}

// Variables describe the variables for a pipeline
type Variables struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ObjectAttributes describe the ObjectAttributes for a pipeline
type ObjectAttributes struct {
	ID         int         `json:"id"`
	Ref        string      `json:"ref"`
	Tag        bool        `json:"tag"`
	Sha        string      `json:"sha"`
	BeforeSha  string      `json:"before_sha"`
	Source     string      `json:"source"`
	Status     string      `json:"status"`
	Stages     []string    `json:"stages"`
	CreatedAt  string      `json:"created_at"`
	FinishedAt string      `json:"finished_at"`
	Duration   int         `json:"duration"`
	Variables  []Variables `json:"variables"`
}

// MergeRequest describe the MergeRequest for a pipeline
type MergeRequest struct {
	ID              int    `json:"id"`
	Iid             int    `json:"iid"`
	Title           string `json:"title"`
	SourceBranch    string `json:"source_branch"`
	SourceProjectID int    `json:"source_project_id"`
	TargetBranch    string `json:"target_branch"`
	TargetProjectID int    `json:"target_project_id"`
	State           string `json:"state"`
	MergeStatus     string `json:"merge_status"`
	URL             string `json:"url"`
}

// UserInfo describe the UserInfo for a pipeline
type UserInfo struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email"`
}

// Project describe the Project for a pipeline
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
	AvatarURL         string `json:"avatar_url"`
	GitSSHURL         string `json:"git_ssh_url"`
	GitHTTPURL        string `json:"git_http_url"`
	Namespace         string `json:"namespace"`
	VisibilityLevel   int    `json:"visibility_level"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
}

// Author describe the Author for a pipeline
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Commit describe the Commit for a pipeline
type Commit struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
	Author    Author    `json:"author"`
}

// User describe the User for a pipeline
type User struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
}

// Builds describe the Builds for a pipeline
type Builds struct {
	ID           int    `json:"id"`
	Stage        string `json:"stage"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	When         string `json:"when"`
	Manual       bool   `json:"manual"`
	AllowFailure bool   `json:"allow_failure"`
	User         User   `json:"user"`
}
