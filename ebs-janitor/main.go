// Package main contains the implementation of an AWS Lambda function that handles CloudWatch events
// related to AWS EBS volumes. It defines an EventHandler that checks for volumes in an 'available'
// state and deletes them if they meet certain age criteria. The package provides a means to create a
// new event handler, process events, and perform cleanup actions on AWS resources. It is designed to
// be run as a scheduled task within the AWS environment and includes logging capabilities for monitoring
// and debugging. Additionally, it can operate in a dry run mode for testing without making actual changes.
package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
)

// BuildVersion the version which lambda is running
var BuildVersion = ""

// BuildTime the time of build created
var BuildTime = ""

func main() {
	logger := log.New()
	logger.Out = os.Stdout
	logger.Formatter = &log.JSONFormatter{}

	logger.WithFields(log.Fields{
		"buildVersion": BuildVersion,
		"buildTime":    BuildTime,
	}).Info("Build Info")

	// loads config
	err := LoadConfig(logger)
	if err != nil {
		log.WithError(err).Error("Unable to load config")
	}

	// creates an AWS session
	sess, err := session.NewSessionWithOptions(session.Options{
		Config: aws.Config{
			Region: aws.String(cfg.Region),
		},
	})
	if err != nil {
		log.WithError(err).Error("failed initiate an AWS session")
		return
	}
	// setup the handler
	awsResourcer := NewClient(sess)
	handler := NewEventHandler(cfg.ExpirationDays, awsResourcer, cfg.Debug, logger)
	if cfg.Debug {
		handler.Handle(context.Background(), events.CloudWatchEvent{}) //nolint
		return
	}
	lambda.Start(handler.Handle)
}
