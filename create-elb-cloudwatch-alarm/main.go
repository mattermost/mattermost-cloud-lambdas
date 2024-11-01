// Package main provides functionality to handle CloudWatch events and manage CloudWatch Alarms
// for Elastic Load Balancers (ELBs) within AWS. It processes events, creates or deletes alarms,
// and interfaces with other AWS services as needed.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elbv2"
	log "github.com/sirupsen/logrus"
)

// Detail includes the relevant data from a CloudWatch event for processing.
type Detail struct {
	UserIdentity      UserIdentity      `json:"userIdentity"`
	EventSource       string            `json:"eventSource"`
	EventName         string            `json:"eventName"`
	AwsRegion         string            `json:"awsRegion"`
	RequestParameters RequestParameters `json:"requestParameters"`
	ResponseElements  ResponseElements  `json:"responseElements"`
}

// UserIdentity represents the identity of the user that initiated the event.
type UserIdentity struct {
	Arn       string `json:"arn"`
	AccountID string `json:"accountId"`
	InvokedBy string `json:"invokedBy"`
}

// RequestParameters holds the parameters used in the request that generated the event.
type RequestParameters struct {
	SecurityGroups   []string `json:"securityGroups"`
	Name             string   `json:"name,omitempty"`
	Type             string   `json:"type,omitempty"`
	LoadBalancerName string   `json:"loadBalancerName,omitempty"`
	Scheme           string   `json:"scheme"`
	LoadBalancerArn  string   `json:"loadBalancerArn,omitempty"`
}

// ResponseElements contains the details of the response for the event.
type ResponseElements struct {
	LoadBalancers []LoadBalancers `json:"loadBalancers,omitempty"`
	DNSName       string          `json:"dNSName,omitempty"`
}

// LoadBalancers defines the structure for load balancer information returned in the event.
type LoadBalancers struct {
	LoadBalancerName string `json:"loadBalancerName"`
	LoadBalancerArn  string `json:"loadBalancerArn"`
}

func main() {
	lambda.Start(handler)
}

func handler(_ context.Context, event events.CloudWatchEvent) {
	log.Infof("Detail = %s\n", event.Detail)

	if event.Source == "aws.elasticloadbalancing" {
		var eventDetail Detail
		err := json.Unmarshal(event.Detail, &eventDetail)
		if err != nil {
			log.WithError(err).Errorln("Error decoding the Event detail")
			return
		}
		log.Infof("eventDetail = %+v\n", eventDetail)

		switch eventDetail.EventName {
		case "CreateLoadBalancer":
			var elbName, targetGroupName string
			elbType := "classic"

			if eventDetail.ResponseElements.DNSName == "" {
				if len(eventDetail.ResponseElements.LoadBalancers) > 0 {
					elbArnName := eventDetail.ResponseElements.LoadBalancers[0].LoadBalancerArn
					elbName = elbArnName[strings.IndexByte(elbArnName, '/')+1:]

					var err error
					targetGroupName, err = getTargetGroup(elbArnName)
					if err != nil {
						log.WithError(err).Errorf("Error getting the target group for lb %s", elbName)
						return
					}

					lb, err := getV2LB(elbArnName)
					if err != nil {
						log.WithError(err).Errorf("Failed to get %s information", elbName)
						return
					}

					if len(lb) == 0 {
						log.Errorf("Expected LB information for %s", elbName)
						return
					}

					if len(lb) > 1 {
						log.Errorf("Expected only one LB for %s", elbName)
						return
					}

					elbType = *lb[0].Type
				} else {
					log.Error("No LoadBalancers found in the event detail")
					return
				}
			} else {
				elbName = eventDetail.RequestParameters.LoadBalancerName
			}

			err := createCloudWatchAlarm(elbName, targetGroupName, elbType)
			if err != nil {
				log.WithError(err).Errorln("Error creating the CloudWatch Alarm")
				return
			}
		case "DeleteLoadBalancer":
			var elbName string
			if eventDetail.RequestParameters.LoadBalancerName == "" {
				elbArnName := eventDetail.RequestParameters.LoadBalancerArn
				elbName = elbArnName[strings.IndexByte(elbArnName, '/')+1:]
			} else {
				elbName = eventDetail.RequestParameters.LoadBalancerName
			}
			err := deleteCloudWatchAlarm(elbName)
			if err != nil {
				log.WithError(err).Errorln("Error deleting the CloudWatch Alarm")
				return
			}
		default:
			log.Infof("Event did not match. Event = %s", eventDetail.EventName)
		}

		return
	}

	listELBs()
}

func listELBs() error {
	v2LBS, classicLBs, err := listAllLBs()
	if err != nil {
		log.WithError(err).Errorln("Failed to get the v2 LBs")
		return err
	}

	var targetGroupName string

	for _, loadBalancer := range v2LBS {
		elbArnName := *loadBalancer.LoadBalancerArn
		elbName := elbArnName[strings.IndexByte(elbArnName, '/')+1:]
		log.Infof("Creating CloudWatch Alarm for %+v/%+v\n", *loadBalancer.LoadBalancerName, *loadBalancer.DNSName)

		targetGroupName, err = getTargetGroup(elbArnName) // Assign to already-declared variables
		if err != nil {
			log.WithError(err).Errorf("Error getting the target group for lb %s", elbName)
			continue
		}

		err = createCloudWatchAlarm(elbName, targetGroupName, *loadBalancer.Type)
		if err != nil {
			log.WithError(err).Errorf("Error creating the CloudWatch Alarm for ELB %s", *loadBalancer.LoadBalancerName)
			continue
		}
	}

	for _, loadBalancer := range classicLBs {
		log.Infof("Creating CloudWatch Alarm for %+v/%+v\n", *loadBalancer.LoadBalancerName, *loadBalancer.DNSName)
		err = createCloudWatchAlarm(*loadBalancer.LoadBalancerName, "", "classic")
		if err != nil {
			log.WithError(err).Errorf("Error creating the CloudWatch Alarm for ELB %s", *loadBalancer.LoadBalancerName)
			continue
		}
	}

	return nil
}

func createCloudWatchAlarm(elbName, targetGroupName, lbType string) error {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		log.WithError(err).Errorln("Error creating aws session")
		return err
	}

	newMetricAlarm := &cloudwatch.PutMetricAlarmInput{
		ActionsEnabled:     aws.Bool(true),
		MetricName:         aws.String("HealthyHostCount"),
		AlarmName:          aws.String(fmt.Sprintf("Alarm-%s", elbName)),
		ComparisonOperator: aws.String(cloudwatch.ComparisonOperatorLessThanOrEqualToThreshold),
		EvaluationPeriods:  aws.Int64(1),
		Period:             aws.Int64(300),
		Statistic:          aws.String(cloudwatch.StatisticAverage),
		Threshold:          aws.Float64(0.0),
		AlarmDescription:   aws.String("Alarm when having at least one unhealthy host"),
		AlarmActions:       []*string{aws.String(os.Getenv("SNS_TOPIC"))},
		OKActions:          []*string{aws.String(os.Getenv("SNS_TOPIC"))},
	}

	if lbType == "classic" {
		newMetricAlarm.Namespace = aws.String("AWS/ELB")
		newMetricAlarm.Dimensions = []*cloudwatch.Dimension{
			{Name: aws.String("LoadBalancerName"), Value: aws.String(elbName)},
		}
	} else {
		typeLB := "AWS/ApplicationELB"
		if lbType == "network" {
			typeLB = "AWS/NetworkELB"
		}
		newMetricAlarm.Namespace = aws.String(typeLB)
		newMetricAlarm.Dimensions = []*cloudwatch.Dimension{
			{Name: aws.String("LoadBalancer"), Value: aws.String(elbName)},
			{Name: aws.String("TargetGroup"), Value: aws.String(targetGroupName)},
		}
	}

	svc := cloudwatch.New(sess)
	_, err = svc.PutMetricAlarm(newMetricAlarm)
	if err != nil {
		log.WithError(err).Errorln("Error creating aws cloudwatch alarm")
		return err
	}

	return nil
}

func deleteCloudWatchAlarm(elbName string) error {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		log.WithError(err).Errorln("Error creating aws session")
		return err
	}

	svc := cloudwatch.New(sess)
	_, err = svc.DeleteAlarms(&cloudwatch.DeleteAlarmsInput{
		AlarmNames: []*string{aws.String(fmt.Sprintf("Alarm-%s", elbName))},
	})
	if err != nil {
		log.WithError(err).Errorln("Error deleting aws cloudwatch alarm")
		return err
	}

	return nil
}

func getTargetGroup(loadBalancerArn string) (string, error) {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		log.WithError(err).Errorln("Error creating aws session")
		return "", err
	}

	svcELBV2 := elbv2.New(sess)
	input := &elbv2.DescribeTargetGroupsInput{LoadBalancerArn: aws.String(loadBalancerArn)}
	targetGroups, err := svcELBV2.DescribeTargetGroups(input)
	if err != nil {
		log.WithError(err).Errorf("Error describing the target groups for lb %s", loadBalancerArn)
		return "", err
	}
	if len(targetGroups.TargetGroups) == 0 {
		return "", fmt.Errorf("No target groups found for lb %s", loadBalancerArn)
	}

	targetGroupArn := *targetGroups.TargetGroups[0].TargetGroupArn
	targetGroupName := targetGroupArn[strings.LastIndexByte(targetGroupArn, ':')+1:]

	return targetGroupName, nil
}

func listAllLBs() ([]*elbv2.LoadBalancer, []*elb.LoadBalancerDescription, error) {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		log.WithError(err).Errorln("Error creating aws session")
		return nil, nil, err
	}

	svcELBV2 := elbv2.New(sess)
	var lbs []*elbv2.LoadBalancer
	for {
		resp, err := svcELBV2.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{})
		if err != nil {
			return nil, nil, err
		}
		lbs = append(lbs, resp.LoadBalancers...)
		if resp.NextMarker == nil {
			break
		}
	}

	svcELB := elb.New(sess)
	var classicELBs []*elb.LoadBalancerDescription
	for {
		resp, err := svcELB.DescribeLoadBalancers(&elb.DescribeLoadBalancersInput{})
		if err != nil {
			return nil, nil, err
		}
		classicELBs = append(classicELBs, resp.LoadBalancerDescriptions...)
		if resp.NextMarker == nil {
			break
		}
	}

	return lbs, classicELBs, nil
}

func getV2LB(lbARN string) ([]*elbv2.LoadBalancer, error) {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		log.WithError(err).Errorln("Error creating aws session")
		return nil, err
	}

	svcELBV2 := elbv2.New(sess)
	resp, err := svcELBV2.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: aws.StringSlice([]string{lbARN}),
	})
	if err != nil {
		return nil, err
	}

	return resp.LoadBalancers, nil
}
