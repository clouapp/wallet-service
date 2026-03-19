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
