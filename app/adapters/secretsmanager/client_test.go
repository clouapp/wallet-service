package secretsmanager

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil reader when Secrets Manager is not configured")
	}
}

func TestNil_Client_ReportsAMissingClient(t *testing.T) {
	var client *Client
	if _, err := client.Binary(context.Background(), "vault/wallet/x/share-b"); err == nil {
		t.Fatal("expected error for a nil client")
	}
}

func TestBinary_Rejects_MissingContextAndSecretID(t *testing.T) {
	client := &Client{api: &fakeAPI{binary: []byte{0x01}}}
	if _, err := client.Binary(nil, "vault/wallet/x/share-b"); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if _, err := client.Binary(context.Background(), ""); err == nil {
		t.Fatal("expected error for an empty secret id")
	}
}

func TestBinary_Reads_TheGivenSecretAndReturnsItsBytes(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03, 0x04}
	fake := &fakeAPI{binary: want}
	client := &Client{api: fake}

	got, err := client.Binary(context.Background(), "vault/wallet/x/share-b")
	if err != nil {
		t.Fatal("read failed")
	}
	if fake.input == nil || aws.ToString(fake.input.SecretId) != "vault/wallet/x/share-b" {
		t.Fatal("secret id changed")
	}
	if fake.input.VersionId != nil || fake.input.VersionStage != nil {
		t.Fatal("GetSecretValue gained extra fields")
	}
	if !bytes.Equal(got, want) {
		t.Fatal("secret bytes changed")
	}
}

func TestBinary_Forwards_TheAPIError(t *testing.T) {
	fake := &fakeAPI{err: errors.New("boom")}
	_, err := (&Client{api: fake}).Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil || err.Error() != "boom" {
		t.Fatal("API error was not returned unchanged")
	}
}

func TestBinary_Rejects_AnEmptyResponse(t *testing.T) {
	_, err := (&Client{api: &fakeAPI{}}).Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil {
		t.Fatal("expected error for an empty response")
	}
}

func TestBinary_Forwards_TheSecretIDUnchanged(t *testing.T) {
	fake := &fakeAPI{binary: []byte{0x01}}
	if _, err := (&Client{api: fake}).Binary(context.Background(), "  vault/wallet/x/share-b  "); err != nil {
		t.Fatal("read failed")
	}
	if fake.input == nil || aws.ToString(fake.input.SecretId) != "  vault/wallet/x/share-b  " {
		t.Fatal("secret id changed")
	}
}

type fakeAPI struct {
	input  *awssm.GetSecretValueInput
	binary []byte
	err    error
}

func (f *fakeAPI) GetSecretValue(_ context.Context, input *awssm.GetSecretValueInput, _ ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error) {
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	if f.binary == nil {
		return nil, nil
	}
	return &awssm.GetSecretValueOutput{SecretBinary: f.binary}, nil
}
