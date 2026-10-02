package seeds

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/macrowallets/waas/app/models"
)

type fakeSeedSecretsManager struct {
	output *secretsmanager.GetSecretValueOutput
	err    error
}

func (f *fakeSeedSecretsManager) GetSecretValue(
	context.Context,
	*secretsmanager.GetSecretValueInput,
	...func(*secretsmanager.Options),
) (*secretsmanager.GetSecretValueOutput, error) {
	return f.output, f.err
}

func TestValidateExistingSeedWalletSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/test/share-b",
	}
	manager := &fakeSeedSecretsManager{
		output: &secretsmanager.GetSecretValueOutput{SecretBinary: []byte("share-b")},
	}

	if err := validateExistingSeedWalletSecret(context.Background(), manager, wallet); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsMissingSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:missing",
	}
	manager := &fakeSeedSecretsManager{
		err: &types.ResourceNotFoundException{Message: stringPointer("missing")},
	}

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil {
		t.Fatal("expected missing share_B error")
	}
	if !strings.Contains(err.Error(), "share_B") || !strings.Contains(err.Error(), "reset PostgreSQL and Secrets Manager together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{MPCSecretARN: "arn:empty"}
	manager := &fakeSeedSecretsManager{
		output: &secretsmanager.GetSecretValueOutput{},
	}

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil || !strings.Contains(err.Error(), "empty share_B") {
		t.Fatalf("expected empty share_B error, got %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	if err := validateExistingSeedWalletSecret(context.Background(), nil, &models.Wallet{}); err == nil {
		t.Fatal("expected nil manager error")
	}
	manager := &fakeSeedSecretsManager{err: errors.New("unexpected")}
	if err := validateExistingSeedWalletSecret(context.Background(), manager, nil); err == nil {
		t.Fatal("expected nil wallet error")
	}
}

func stringPointer(value string) *string {
	return &value
}
