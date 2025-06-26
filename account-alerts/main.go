// Package main defines an AWS Lambda function that monitors the IP address utilization of subnets within AWS VPCs.
// It checks for provisioning subnets reaching IP address capacity and sends notifications if the number of
// available IP addresses falls below a defined threshold. The function is triggered to evaluate the environment
// variables for configuration, establish a new AWS session, check the IAM role's permissions, and iterate
// through subnets within VPCs to assess and report on IP address availability. Notifications for any identified
// issues are sent to a configured Mattermost channel.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"

	log "github.com/sirupsen/logrus"
)

type environmentVariables struct {
	MinSubnetFreeIPs int64
}

func main() {
	lambda.Start(handler)
}

func handler() {

	envVars, err := validateAndGetEnvVars()
	if err != nil {
		log.WithError(err).Error("Environment variable validation failed")
		err = sendMattermostErrorNotification(err, "Environment variable validation failed")
		if err != nil {
			log.WithError(err).Error("Failed to send Mattermost error notification")
		}
		os.Exit(1)
	}

	log.Info("Getting existing Provisioning Subnet IP limits")
	err = checkProvisioningSubnetIPLimits(*envVars)
	if err != nil {
		log.WithError(err).Error("Unable to get the number of available VPCs")
	}
}

// validateEnvironmentVariables is used to validate the environment variables needed by Blackbox target discovery.
func validateAndGetEnvVars() (*environmentVariables, error) {
	envVars := &environmentVariables{}
	minSubnetFreeIPs := os.Getenv("MIN_SUBNET_FREE_IPs")
	if len(minSubnetFreeIPs) == 0 {
		return nil, errors.Errorf("MIN_SUBNET_FREE_IPs environment variable is not set")
	}

	number, err := strconv.Atoi(minSubnetFreeIPs)
	if err != nil {
		return nil, err
	}
	envVars.MinSubnetFreeIPs = int64(number)

	return envVars, nil
}

// getSetProvisioningSubnetIPLimits is used to get the Provisioning VPCs Subnet IP limits and set the CW metric data.
func checkProvisioningSubnetIPLimits(envVars environmentVariables) error {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		return err
	}

	svc := ec2.New(sess)

	vpcs, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("tag:Available"),
				Values: []*string{
					aws.String("false"),
				},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, vpc := range vpcs.Vpcs {
		log.Infof("Exploring VPC %s", *vpc.VpcId)
		subnets, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
			Filters: []*ec2.Filter{
				{
					Name: aws.String("vpc-id"),
					Values: []*string{
						aws.String(*vpc.VpcId),
					},
				},
			},
		})
		if err != nil {
			return err
		}
		for _, subnet := range subnets.Subnets {
			if *subnet.AvailableIpAddressCount < envVars.MinSubnetFreeIPs {
				log.Infof("Subnet %s has low number of available IPs (%d)", *subnet.SubnetId, *subnet.AvailableIpAddressCount)
				err := sendMattermostAlertNotification(fmt.Sprintf("Subnet %s has low number of available IPs (%d)", *subnet.SubnetId, *subnet.AvailableIpAddressCount), "VPC Subnets")
				if err != nil {
					log.WithError(err).Error("Failed to send Mattermost alert notification")
				}
			}
		}
	}

	return nil
}
