package secretsmanager

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func TestNewClientUsesTheDefaultResolverWhenTheEndpointIsEmpty(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1"}, "")
	if client == nil {
		t.Fatal("expected a client")
	}
}

func TestNewClientKeepsACustomEndpoint(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1"}, "http://127.0.0.1:4566")
	if client == nil {
		t.Fatal("expected a client")
	}
}

func TestEndpointResolverRejectsABadURL(t *testing.T) {
	_, err := endpointResolver{url: "://bad"}.ResolveEndpoint(context.Background(), awssm.EndpointParameters{})
	if err == nil {
		t.Fatal("expected a URL parse error")
	}
}
