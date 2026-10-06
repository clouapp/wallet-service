package secretsmanager

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func TestNew_Wallet_StoreReturnsNilForANilClient(t *testing.T) {
	if NewWalletStore(nil) != nil {
		t.Fatal("expected a nil store when Secrets Manager is not configured")
	}
}

func TestNil_Wallet_StoreReportsAMissingClient(t *testing.T) {
	var store *WalletStore
	if _, err := store.Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01}); err == nil {
		t.Fatal("expected error for a nil client")
	}
	if _, err := store.Binary(context.Background(), "vault/wallet/x/share-b"); err == nil {
		t.Fatal("expected error for a nil client")
	}
}

func TestCreate_Stores_TheGivenNameAndBytes(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03, 0x04}
	fake := &fakeWalletAPI{arn: "arn:aws:secretsmanager:us-east-1:1:secret:vault/wallet/x/share-b"}
	store := &WalletStore{api: fake}

	arn, err := store.Create(context.Background(), "vault/wallet/x/share-b", want)
	if err != nil {
		t.Fatal("create failed")
	}
	if arn != fake.arn {
		t.Fatal("ARN changed")
	}
	if fake.create == nil || aws.ToString(fake.create.Name) != "vault/wallet/x/share-b" {
		t.Fatal("secret name changed")
	}
	if fake.create.Description != nil || fake.create.SecretString != nil || fake.create.KmsKeyId != nil || fake.create.ClientRequestToken != nil || len(fake.create.Tags) != 0 {
		t.Fatal("CreateSecret gained extra fields")
	}
	if !bytes.Equal(fake.create.SecretBinary, want) || &fake.create.SecretBinary[0] != &want[0] {
		t.Fatal("secret bytes changed")
	}
}

func TestCreate_Forwards_TheNameUnchanged(t *testing.T) {
	fake := &fakeWalletAPI{arn: "arn:aws:secretsmanager:us-east-1:1:secret:padded"}
	if _, err := (&WalletStore{api: fake}).Create(context.Background(), "  vault/wallet/x/share-b  ", []byte{0x01}); err != nil {
		t.Fatal("create failed")
	}
	if fake.create == nil || aws.ToString(fake.create.Name) != "  vault/wallet/x/share-b  " {
		t.Fatal("secret name changed")
	}
}

func TestCreate_Forwards_TheAPIError(t *testing.T) {
	fake := &fakeWalletAPI{err: errors.New("boom")}
	_, err := (&WalletStore{api: fake}).Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01})
	if err == nil || err.Error() != "boom" {
		t.Fatal("API error was not returned unchanged")
	}
}

func TestCreate_Returns_AnEmptyARNWhenTheResponseHasNone(t *testing.T) {
	arn, err := (&WalletStore{api: &fakeWalletAPI{}}).Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01})
	if err != nil {
		t.Fatal("create failed")
	}
	if arn != "" {
		t.Fatal("missing ARN was not returned empty")
	}
}

func TestWallet_Store_BinaryReadsTheGivenSecretAndReturnsItsBytes(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03, 0x04}
	fake := &fakeWalletAPI{binary: want}
	store := &WalletStore{api: fake}

	got, err := store.Binary(context.Background(), "vault/wallet/x/share-b")
	if err != nil {
		t.Fatal("read failed")
	}
	if fake.get == nil || aws.ToString(fake.get.SecretId) != "vault/wallet/x/share-b" {
		t.Fatal("secret id changed")
	}
	if fake.get.VersionId != nil || fake.get.VersionStage != nil {
		t.Fatal("GetSecretValue gained extra fields")
	}
	if !bytes.Equal(got, want) || &got[0] != &want[0] {
		t.Fatal("secret bytes changed")
	}
}

func TestWallet_Store_BinaryForwardsTheSecretIDUnchanged(t *testing.T) {
	fake := &fakeWalletAPI{binary: []byte{0x01}}
	if _, err := (&WalletStore{api: fake}).Binary(context.Background(), "  vault/wallet/x/share-b  "); err != nil {
		t.Fatal("read failed")
	}
	if fake.get == nil || aws.ToString(fake.get.SecretId) != "  vault/wallet/x/share-b  " {
		t.Fatal("secret id changed")
	}
}

func TestWallet_Store_BinaryForwardsTheAPIError(t *testing.T) {
	fake := &fakeWalletAPI{err: errors.New("boom")}
	_, err := (&WalletStore{api: fake}).Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil || err.Error() != "boom" {
		t.Fatal("API error was not returned unchanged")
	}
}

type fakeWalletAPI struct {
	create *awssm.CreateSecretInput
	get    *awssm.GetSecretValueInput
	arn    string
	binary []byte
	err    error
}

func (f *fakeWalletAPI) CreateSecret(_ context.Context, input *awssm.CreateSecretInput, _ ...func(*awssm.Options)) (*awssm.CreateSecretOutput, error) {
	f.create = input
	if f.err != nil {
		return nil, f.err
	}
	if f.arn == "" {
		return &awssm.CreateSecretOutput{}, nil
	}
	return &awssm.CreateSecretOutput{ARN: aws.String(f.arn)}, nil
}

func (f *fakeWalletAPI) GetSecretValue(_ context.Context, input *awssm.GetSecretValueInput, _ ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error) {
	f.get = input
	if f.err != nil {
		return nil, f.err
	}
	return &awssm.GetSecretValueOutput{SecretBinary: f.binary}, nil
}
