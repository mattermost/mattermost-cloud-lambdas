// Package main provides a log forwarding Lambda function that processes events from AWS services like S3 and CloudWatch Logs.
// It parses the incoming events, applies additional labels as configured, and forwards them to a Loki instance for log aggregation.
// The function supports dynamic client creation for S3 based on the event source, and batch processing for log events.
// It also allows for optional inclusion of the original log message as a label and retention of the log stream information.
// This package requires several environment variables to be set for proper configuration, including the Loki API endpoint,
// authentication credentials, and any extra labels for log enrichment. It is designed to be deployed as an AWS Lambda function.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/common/model"
	log "github.com/sirupsen/logrus"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	// We use snappy-encoded protobufs over http by default.
	contentType = "application/x-protobuf"

	maxErrMsgLen = 1024

	invalidExtraLabelsError = "invalid value for environment variable EXTRA_LABELS. Expected a comma separated list with an even number of entries. "
)

var (
	writeAddress                                 *url.URL
	username, password, extraLabelsRaw, tenantID string
	keepStream                                   bool
	includeMessageAsLabel                        bool
	batchSize                                    int
	s3Clients                                    map[string]*s3.Client
	extraLabels                                  model.LabelSet
)

func setupArguments() {
	var err error

	addr := os.Getenv("WRITE_ADDRESS")
	if addr == "" {
		log.WithError(err).Error("required environmental variable WRITE_ADDRESS not present, format: https://<hostname>/loki/api/v1/push")
	}

	writeAddress, err = url.Parse(addr)
	if err != nil {
		log.WithError(err).Error("unable to parse URL provided")
	}

	fmt.Println("write address: ", writeAddress.String())

	extraLabelsRaw = os.Getenv("EXTRA_LABELS")
	extraLabels, err = parseExtraLabels(extraLabelsRaw)
	if err != nil {
		log.WithError(err)
	}

	username = os.Getenv("USERNAME")
	password = os.Getenv("PASSWORD")
	// If either username or password is set then both must be.
	if (username != "" && password == "") || (username == "" && password != "") {
		log.WithError(err).Error("both username and password must be set if either one is set")
	}

	tenantID = os.Getenv("TENANT_ID")

	keep := os.Getenv("KEEP_STREAM")
	// Anything other than case-insensitive 'true' is treated as 'false'.
	if strings.EqualFold(keep, "true") {
		keepStream = true
	}
	fmt.Println("keep stream: ", keepStream)

	messageIncluded := os.Getenv("INCLUDE_MESSAGE")
	// Anything other than case-insensitive 'true' is treated as 'false'.
	if strings.EqualFold(messageIncluded, "true") {
		includeMessageAsLabel = true
	}
	fmt.Println("Include Message As Label: ", includeMessageAsLabel)

	batch := os.Getenv("BATCH_SIZE")
	batchSize = 131072
	if batch != "" {
		batchSize, _ = strconv.Atoi(batch)
	}

	s3Clients = make(map[string]*s3.Client)
}

func parseExtraLabels(extraLabelsRaw string) (model.LabelSet, error) {
	var extractedLabels = model.LabelSet{}
	extraLabelsSplit := strings.Split(extraLabelsRaw, ",")

	if len(extraLabelsRaw) < 1 {
		return extractedLabels, nil
	}

	if len(extraLabelsSplit)%2 != 0 {
		return nil, errors.New(invalidExtraLabelsError)
	}
	for i := 0; i < len(extraLabelsSplit); i += 2 {
		extractedLabels[model.LabelName("__extra_"+extraLabelsSplit[i])] = model.LabelValue(extraLabelsSplit[i+1])
	}
	err := extractedLabels.Validate()
	if err != nil {
		return nil, err
	}
	fmt.Println("extra labels:", extractedLabels)
	return extractedLabels, nil
}

func applyExtraLabels(labels model.LabelSet) model.LabelSet {
	return labels.Merge(extraLabels)
}

func checkEventType(ev map[string]interface{}) (interface{}, error) {
	var s3Event events.S3Event
	var cwEvent events.CloudwatchLogsEvent

	types := [...]interface{}{&s3Event, &cwEvent}

	j, _ := json.Marshal(ev)
	reader := strings.NewReader(string(j))
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()

	for _, t := range types {
		err := d.Decode(t)

		if err == nil {
			return t, nil
		}

		_, err = reader.Seek(0, 0)
		if err != nil {
			log.WithError(err)
		}
	}

	return nil, errors.New("unknown event type")
}

func handler(ctx context.Context, ev map[string]interface{}) error {
	event, err := checkEventType(ev)
	if err != nil {
		log.WithError(err)
		return err
	}

	switch event := event.(type) {
	case *events.S3Event:
		return processS3Event(ctx, event)
	case *events.CloudwatchLogsEvent:
		return processCWEvent(ctx, event)
	}

	return err
}

func main() {
	setupArguments()
	lambda.Start(handler)
}
