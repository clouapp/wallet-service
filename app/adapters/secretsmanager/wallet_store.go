package secretsmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/macrowallets/waas/app/services/wallet"
)

// walletAPI is the subset of the AWS Secrets Manager client the wallet store calls.
type walletAPI interface {
	CreateSecret(ctx context.Context, input *awssm.CreateSecretInput, opts ...func(*awssm.Options)) (*awssm.CreateSecretOutput, error)
	GetSecretValue(ctx context.Context, input *awssm.GetSecretValueInput, opts ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error)
	DescribeSecret(ctx context.Context, input *awssm.DescribeSecretInput, opts ...func(*awssm.Options)) (*awssm.DescribeSecretOutput, error)
}

// WalletStore writes and reads one secret for the wallet service.
// The service keeps the secret name and the bytes. Values are not logged.
type WalletStore struct {
	api      walletAPI
	kmsKeyID string
}

var _ wallet.SecretStore = (*WalletStore)(nil)
var _ walletAPI = (*awssm.Client)(nil)

// NewWalletStore wraps client. Shares it creates are sealed with kmsKeyID (see
// KMSKeyID); an empty one leaves Secrets Manager on its default key. A nil client
// returns a nil store so the service keeps its nil-client path.
func NewWalletStore(client *awssm.Client, kmsKeyID string) wallet.SecretStore {
	if client == nil {
		return nil
	}
	return &WalletStore{api: client, kmsKeyID: kmsKeyID}
}

// NewShareStore is NewWalletStore with the concrete type, for callers that also look a
// share up by name (secrets:migrate-shares). A nil client gives a store whose calls
// report the missing client.
func NewShareStore(client *awssm.Client, kmsKeyID string) *WalletStore {
	if client == nil {
		return &WalletStore{}
	}
	return &WalletStore{api: client, kmsKeyID: kmsKeyID}
}

// Create stores secretBinary under name and returns the ARN. The name, the bytes, and the AWS error are unchanged.
func (s *WalletStore) Create(ctx context.Context, name string, secretBinary []byte) (string, error) {
	if s == nil || s.api == nil {
		return "", fmt.Errorf("secrets manager: client is nil")
	}
	input := &awssm.CreateSecretInput{
		Name:         aws.String(name),
		SecretBinary: secretBinary,
	}
	if s.kmsKeyID != "" {
		input.KmsKeyId = aws.String(s.kmsKeyID)
	}
	out, err := s.api.CreateSecret(ctx, input)
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

// Lookup returns the ARN of the secret named name, and false when there is none.
// Any other AWS error is returned unchanged.
func (s *WalletStore) Lookup(ctx context.Context, name string) (string, bool, error) {
	if s == nil || s.api == nil {
		return "", false, fmt.Errorf("secrets manager: client is nil")
	}
	out, err := s.api.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String(name)})
	var missing *smtypes.ResourceNotFoundException
	if errors.As(err, &missing) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return aws.ToString(out.ARN), true, nil
}
