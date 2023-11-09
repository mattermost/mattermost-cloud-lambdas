// Package main defines an AWS Lambda function that identifies and cleans up unused Elastic Load Balancers (ELBs)
// and Classic Load Balancers within AWS. The function checks for load balancers that are not in use and deletes them
// to avoid unnecessary costs. It logs build information such as the version and build time, loads necessary configurations,
// and establishes an AWS session. The function can be configured to run in a dry-run mode, where it only logs the load
// balancers that would be deleted without actually performing the deletion. This function is triggered by CloudWatch events.
package main

import (
	"os"

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

	dryrun := os.Getenv("dryrun")
	if len(dryrun) > 0 && dryrun == "true" {
		log.Info("Running in a dryrun mode")
		cfg.Debug = true
	}

	// setup the handler
	awsResourcer := NewClient(sess)
	handler := NewEventHandler(awsResourcer, cfg.Debug, logger)

	lambda.Start(handler.Handle)
}
