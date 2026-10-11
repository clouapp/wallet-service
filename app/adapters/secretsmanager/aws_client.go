package secretsmanager

import (
	"context"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
)

// SDKClient is the AWS Secrets Manager client. Services never name this type.
// AppServiceProvider binds one, which key export resolves.
type SDKClient = awssm.Client

type endpointResolver struct{ url string }

func (r endpointResolver) ResolveEndpoint(
	_ context.Context,
	_ awssm.EndpointParameters,
) (smithyendpoints.Endpoint, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{URI: *u}, nil
}

// NewClient builds the AWS client. An empty endpoint uses AWS itself: an endpoint
// the process environment or a profile set (AWS_ENDPOINT_URL) is dropped, so
// vault.aws.endpoint_url is the only thing that can point the client elsewhere.
func NewClient(cfg aws.Config, endpoint string) *SDKClient {
	if endpoint == "" {
		return awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = nil })
	}
	return awssm.NewFromConfig(cfg, awssm.WithEndpointResolverV2(endpointResolver{url: endpoint}))
}
