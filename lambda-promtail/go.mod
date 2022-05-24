module github.com/mattermost/mattermost-cloud-lambdas/lambda-promtail

go 1.18

require (
	github.com/aws/aws-lambda-go v1.32.0
	github.com/aws/aws-sdk-go-v2 v1.16.4
	github.com/aws/aws-sdk-go-v2/config v1.15.7
	github.com/aws/aws-sdk-go-v2/service/s3 v1.26.10
	github.com/gogo/protobuf v1.3.2
	github.com/golang/snappy v0.0.4
	github.com/grafana/dskit v0.0.0-20220524131630-f7f374ef9c0e
	github.com/grafana/loki v1.6.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/common v0.34.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.1
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.1 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.12.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.12.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.0.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.13.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.11.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.16.6 // indirect
	github.com/aws/smithy-go v1.11.2 // indirect
	github.com/cespare/xxhash v1.1.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/prometheus v1.8.2-0.20200727090838-6f296594a852 // indirect
	golang.org/x/net v0.0.0-20220225172249-27dd8689420f // indirect
	golang.org/x/sys v0.0.0-20220114195835-da31bd327af9 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20210602131652-f16073e35f0c // indirect
	google.golang.org/grpc v1.38.0 // indirect
	google.golang.org/protobuf v1.26.0 // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

replace (
	github.com/cristalhq/hedgedhttp => github.com/cristalhq/hedgedhttp v0.7.0
	github.com/stretchr/testify => github.com/stretchr/testify v1.6.1
)
