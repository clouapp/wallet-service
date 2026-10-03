package secretsmanager

import (
	"context"
	"fmt"

	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/macrowallets/waas/app/services/sweep"
)

// api is the subset of the AWS Secrets Manager client this adapter calls.
type api interface {
	GetSecretValue(ctx context.Context, input *awssm.GetSecretValueInput, opts ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error)
}

// Client reads one secret with GetSecretValue. The service keeps the secret id.
// The binary value is not logged.
type Client struct {
	api api
}

var _ sweep.SecretReader = (*Client)(nil)
var _ api = (*awssm.Client)(nil)

// New wraps client. A nil client returns a nil reader so the service keeps its nil-client path.
func New(client *awssm.Client) sweep.SecretReader {
	if client == nil {
		return nil
	}
	return &Client{api: client}
}

// Binary returns the secret bytes for secretID. The id and the AWS error are unchanged.
func (c *Client) Binary(ctx context.Context, secretID string) ([]byte, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("secrets manager: client is nil")
	}
	if ctx == nil {
		return nil, fmt.Errorf("secrets manager: context is nil")
	}
	if secretID == "" {
		return nil, fmt.Errorf("secrets manager: secret id is required")
	}
	out, err := c.api.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: &secretID,
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("secrets manager: empty response")
	}
	return out.SecretBinary, nil
}
