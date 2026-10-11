package secretsmanager

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func TestNew_Client_UsesTheDefaultResolverWhenTheEndpointIsEmpty(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1"}, "")
	if client == nil {
		t.Fatal("expected a client")
	}
}

func TestNew_Client_KeepsACustomEndpoint(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1"}, "http://127.0.0.1:4566")
	if client == nil {
		t.Fatal("expected a client")
	}
}

func TestEndpoint_Resolver_RejectsABadURL(t *testing.T) {
	_, err := endpointResolver{url: "://bad"}.ResolveEndpoint(context.Background(), awssm.EndpointParameters{})
	if err == nil {
		t.Fatal("expected a URL parse error")
	}
}

// An endpoint the process environment or a profile set (AWS_ENDPOINT_URL) must not
// redirect the client: vault.aws.endpoint_url is the only authority.
func TestNew_Client_IgnoresAnInheritedEndpoint(t *testing.T) {
	client := NewClient(aws.Config{Region: "us-east-1", BaseEndpoint: aws.String("http://127.0.0.1:4566")}, "")
	if endpoint := client.Options().BaseEndpoint; endpoint != nil {
		t.Fatalf("BaseEndpoint = %q, want the AWS default", *endpoint)
	}
}
