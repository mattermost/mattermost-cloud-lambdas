# Cloudwatch Event Alerts

This is a lambda function that gets triggered by SNS messages registered with Cloudwatch Rules. Once a rule is triggered an SNS message hits the Lambda function, which pushes the alert to Mattermost and PagerDuty. 
