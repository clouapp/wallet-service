package mocks

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// MockSecretsManager is an in-memory mock for AWS Secrets Manager.
type MockSecretsManager struct {
	secrets map[string][]byte
}

func NewMockSecretsManager() *MockSecretsManager {
	return &MockSecretsManager{secrets: make(map[string][]byte)}
}

func (m *MockSecretsManager) CreateSecret(_ context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	name := aws.ToString(input.Name)
	if _, exists := m.secrets[name]; exists {
		return nil, fmt.Errorf("secret already exists: %s", name)
	}
	m.secrets[name] = input.SecretBinary
	arn := "arn:aws:secretsmanager:us-east-1:000000000000:secret:" + name
	return &secretsmanager.CreateSecretOutput{
		ARN:  aws.String(arn),
		Name: aws.String(name),
	}, nil
}

// Create stores secretBinary under name and returns the same ARN CreateSecret would.
func (m *MockSecretsManager) Create(ctx context.Context, name string, secretBinary []byte) (string, error) {
	out, err := m.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretBinary: secretBinary,
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.ARN), nil
}

// Binary returns the stored bytes for secretID. The slice is the one Create stored.
func (m *MockSecretsManager) Binary(ctx context.Context, secretID string) ([]byte, error) {
	out, err := m.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretID})
	if err != nil {
		return nil, err
	}
	return out.SecretBinary, nil
}

func (m *MockSecretsManager) GetSecretValue(_ context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	secretID := aws.ToString(input.SecretId)
	for name, data := range m.secrets {
		arn := "arn:aws:secretsmanager:us-east-1:000000000000:secret:" + name
		if name == secretID || arn == secretID {
			return &secretsmanager.GetSecretValueOutput{
				SecretBinary: data,
				Name:         aws.String(name),
				ARN:          aws.String(arn),
			}, nil
		}
	}
	return nil, fmt.Errorf("secret not found: %s", secretID)
}
