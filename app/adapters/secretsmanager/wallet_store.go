package secretsmanager

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/macrowallets/waas/app/services/wallet"
)

// walletAPI is the subset of the AWS Secrets Manager client the wallet store calls.
type walletAPI interface {
	CreateSecret(ctx context.Context, input *awssm.CreateSecretInput, opts ...func(*awssm.Options)) (*awssm.CreateSecretOutput, error)
	GetSecretValue(ctx context.Context, input *awssm.GetSecretValueInput, opts ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error)
}

// WalletStore writes and reads one secret for the wallet service.
// The service keeps the secret name and the bytes. Values are not logged.
type WalletStore struct {
	api walletAPI
}

var _ wallet.SecretStore = (*WalletStore)(nil)
var _ walletAPI = (*awssm.Client)(nil)

// NewWalletStore wraps client. A nil client returns a nil store so the service keeps its nil-client path.
func NewWalletStore(client *awssm.Client) wallet.SecretStore {
	if client == nil {
		return nil
	}
	return &WalletStore{api: client}
}

// Create stores secretBinary under name and returns the ARN. The name, the bytes, and the AWS error are unchanged.
func (s *WalletStore) Create(ctx context.Context, name string, secretBinary []byte) (string, error) {
	if s == nil || s.api == nil {
		return "", fmt.Errorf("secrets manager: client is nil")
	}
	out, err := s.api.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String(name),
		SecretBinary: secretBinary,
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.ARN), nil
}

// Binary returns the secret bytes for secretID. The id, the bytes, and the AWS error are unchanged.
func (s *WalletStore) Binary(ctx context.Context, secretID string) ([]byte, error) {
	if s == nil || s.api == nil {
		return nil, fmt.Errorf("secrets manager: client is nil")
	}
	out, err := s.api.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: &secretID,
	})
	if err != nil {
		return nil, err
	}
	return out.SecretBinary, nil
}
