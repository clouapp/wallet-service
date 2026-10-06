package secretsmanager

import (
	"context"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
)

// SDKClient is the AWS Secrets Manager client. Services never name this type.
// The process container still holds one so key export can resolve it.
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

// NewClient builds the AWS client. An empty endpoint uses the default resolver.
func NewClient(cfg aws.Config, endpoint string) *SDKClient {
	if endpoint == "" {
		return awssm.NewFromConfig(cfg)
	}
	return awssm.NewFromConfig(cfg, awssm.WithEndpointResolverV2(endpointResolver{url: endpoint}))
}
