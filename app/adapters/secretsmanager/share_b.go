package secretsmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/keyexport"
)

// secretValueAPI is the subset of the AWS Secrets Manager client ShareB calls.
type secretValueAPI interface {
	GetSecretValue(ctx context.Context, input *awssm.GetSecretValueInput, opts ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error)
}

// ShareB reads a wallet's service share from the secret named by its ARN. The
// caller zeroes the bytes. A provider error is replaced by one that names only
// the wallet, so a message from AWS never reaches a terminal.
type ShareB struct {
	api secretValueAPI
}

var _ keyexport.ShareBSource = (*ShareB)(nil)
var _ secretValueAPI = (*awssm.Client)(nil)

// NewShareB reads through client.
func NewShareB(client *awssm.Client) *ShareB {
	if client == nil {
		return &ShareB{}
	}
	return &ShareB{api: client}
}

func (s *ShareB) FetchShareB(ctx context.Context, wallet models.Wallet) ([]byte, error) {
	if s == nil || s.api == nil {
		return nil, errors.New("secrets manager is not configured")
	}
	arn := strings.TrimSpace(wallet.MPCSecretARN)
	if arn == "" {
		return nil, fmt.Errorf("wallet %s has no MPC secret ARN", wallet.ID)
	}
	out, err := s.api.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: &arn})
	if err != nil {
		return nil, fmt.Errorf("fetch share B of wallet %s", wallet.ID)
	}
	return out.SecretBinary, nil
}
