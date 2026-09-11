package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	log "github.com/sirupsen/logrus"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// signedRequest builds a request carrying a valid X-Hub-Signature-256 for body.
func signedRequest(body string) events.APIGatewayProxyRequest {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(body))

	return events.APIGatewayProxyRequest{
		HTTPMethod: http.MethodPost,
		Headers: map[string]string{
			// Lowercased deliberately: API Gateway v2 and Lambda function URLs
			// lowercase header names, so lookups must stay case-insensitive.
			"x-github-event":      "pull_request_review",
			"x-github-delivery":   "test-delivery",
			"x-hub-signature-256": signaturePrefix + hex.EncodeToString(mac.Sum(nil)),
			"content-type":        "application/json",
		},
		Body: body,
	}
}

func testConfig() *Config {
	return &Config{
		N8NWebhookURL:       "https://n8n.example.invalid/webhook/test",
		GitHubWebhookSecret: testSecret,
		N8NAPIKey:           "0123456789abcdef",
		ForwardTimeout:      defaultForwardTimeout,
	}
}

// captureLogs runs fn with logrus writing to a buffer at the given level.
func captureLogs(level log.Level, fn func()) string {
	var buf bytes.Buffer

	origOut, origLevel, origFormatter := log.StandardLogger().Out, log.GetLevel(), log.StandardLogger().Formatter
	defer func() {
		log.SetOutput(origOut)
		log.SetLevel(origLevel)
		log.SetFormatter(origFormatter)
	}()

	log.SetOutput(&buf)
	log.SetLevel(level)
	log.SetFormatter(&log.JSONFormatter{})

	fn()

	return buf.String()
}

// TestFilteredDeliveryIsLoggedAtInfo is the regression test for the original
// defect: the filtered branch logged at Debug while the function ran at Info in
// Lambda, so a delivery that arrived and authenticated correctly produced no
// application log line and looked identical to one that never arrived.
func TestFilteredDeliveryIsLoggedAtInfo(t *testing.T) {
	// A review submission from a non-cursor author: authenticates, then filters.
	body := `{"action":"submitted","pull_request":{"merged":false,"user":{"login":"somebody"}},"review":{"state":"commented","user":{"login":"somebody"}}}`

	var resp events.APIGatewayProxyResponse
	out := captureLogs(log.InfoLevel, func() {
		var err error
		resp, err = handler(context.Background(), testConfig(), http.DefaultClient, signedRequest(body))
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (should authenticate then filter)", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(resp.Body, `"filtered"`) {
		t.Fatalf("body = %q, want it to report filtered", resp.Body)
	}
	if !strings.Contains(out, "Filtered webhook") {
		t.Fatalf("filtered delivery produced no log line at info level; got %q", out)
	}
	if !strings.Contains(out, "test-delivery") {
		t.Errorf("log line is missing the delivery id; got %q", out)
	}
}

// TestRejectionsAreLogged covers the paths that must never be silent, so a 4xx
// is always explicable from the logs alone.
func TestRejectionsAreLogged(t *testing.T) {
	cases := []struct {
		name       string
		request    events.APIGatewayProxyRequest
		wantStatus int
		wantLog    string
	}{
		{
			name:       "bad signature",
			request:    events.APIGatewayProxyRequest{HTTPMethod: http.MethodPost, Headers: map[string]string{"x-hub-signature-256": signaturePrefix + strings.Repeat("0", 64)}, Body: `{}`},
			wantStatus: http.StatusUnauthorized,
			wantLog:    "signature mismatch",
		},
		{
			name:       "empty body",
			request:    signedRequest(""),
			wantStatus: http.StatusBadRequest,
			wantLog:    "empty request body",
		},
		{
			name:       "malformed json",
			request:    signedRequest(`{not json`),
			wantStatus: http.StatusBadRequest,
			wantLog:    "malformed JSON body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp events.APIGatewayProxyResponse
			out := captureLogs(log.InfoLevel, func() {
				var err error
				resp, err = handler(context.Background(), testConfig(), http.DefaultClient, tc.request)
				if err != nil {
					t.Fatalf("handler returned error: %v", err)
				}
			})

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if !strings.Contains(out, tc.wantLog) {
				t.Errorf("expected a log line containing %q at info level; got %q", tc.wantLog, out)
			}
		})
	}
}

func TestLogLevel(t *testing.T) {
	cases := []struct {
		raw  string
		want log.Level
	}{
		{"", log.InfoLevel},
		{"debug", log.DebugLevel},
		{"DEBUG", log.DebugLevel},
		{"warn", log.WarnLevel},
		{"error", log.ErrorLevel},
		{"not-a-level", log.InfoLevel}, // must fall back, never silence logging
	}

	for _, tc := range cases {
		name := tc.raw
		if name == "" {
			name = "empty"
		}

		t.Run(name, func(t *testing.T) {
			// Swallow the warning that an unparseable value emits.
			var got log.Level
			captureLogs(log.InfoLevel, func() { got = logLevel(tc.raw) })

			if got != tc.want {
				t.Errorf("logLevel(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestShouldForward pins the filter chain, since promoting the filtered branch
// to Info makes its behaviour operationally visible and easy to misread.
func TestShouldForward(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "merged cursor pr forwards",
			body: `{"action":"closed","pull_request":{"merged":true,"user":{"login":"cursor[bot]"}}}`,
			want: true,
		},
		{
			name: "non-approval review on cursor pr forwards",
			body: `{"action":"submitted","pull_request":{"merged":false,"user":{"login":"cursor[bot]"}},"review":{"state":"changes_requested","user":{"login":"someone"}}}`,
			want: true,
		},
		{
			name: "approval is filtered",
			body: `{"action":"submitted","pull_request":{"merged":false,"user":{"login":"cursor[bot]"}},"review":{"state":"approved","user":{"login":"someone"}}}`,
			want: false,
		},
		{
			name: "review by automation user is filtered",
			body: `{"action":"submitted","pull_request":{"merged":false,"user":{"login":"cursor[bot]"}},"review":{"state":"commented","user":{"login":"mattermost-code"}}}`,
			want: false,
		},
		{
			name: "human authored pr is filtered",
			body: `{"action":"submitted","pull_request":{"merged":true,"user":{"login":"somebody"}}}`,
			want: false,
		},
		{
			name: "ping event with no pull_request is filtered",
			body: `{"zen":"Keep it logically awesome."}`,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload gitHubWebhookPayload
			if err := json.Unmarshal([]byte(tc.body), &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got := shouldForward(&payload); got != tc.want {
				t.Errorf("shouldForward() = %v, want %v", got, tc.want)
			}
		})
	}
}
