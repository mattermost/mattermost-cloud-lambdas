module github.com/mattermost/mattermost-cloud-lambdas/lambda-promtail

go 1.18

require (
	github.com/aws/aws-lambda-go v1.41.0
	github.com/aws/aws-sdk-go-v2 v1.22.2
	github.com/aws/aws-sdk-go-v2/config v1.22.3
	github.com/aws/aws-sdk-go-v2/service/s3 v1.42.1
	github.com/gogo/protobuf v1.3.2
	github.com/golang/snappy v0.0.4
	github.com/grafana/dskit v0.0.0-20231106102023-e59db7bb4403
	github.com/grafana/loki v1.6.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/common v0.45.0
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.8.1
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.5.0 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.15.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.14.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.2.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.5.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.5.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.2.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.10.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.2.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.10.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.16.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.17.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.19.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.25.1 // indirect
	github.com/aws/smithy-go v1.16.0 // indirect
	github.com/cespare/xxhash v1.1.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/prometheus v1.8.2-0.20200727090838-6f296594a852 // indirect
	golang.org/x/net v0.18.0 // indirect
	golang.org/x/sys v0.14.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
	google.golang.org/grpc v1.59.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/cristalhq/hedgedhttp => github.com/cristalhq/hedgedhttp v0.7.0
	github.com/stretchr/testify => github.com/stretchr/testify v1.6.1
)
