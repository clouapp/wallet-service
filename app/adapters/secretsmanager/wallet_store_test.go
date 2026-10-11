package secretsmanager

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNew_Wallet_StoreReturnsNilForANilClient(t *testing.T) {
	if NewWalletStore(nil, "") != nil {
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
	const arn = "arn:aws:secretsmanager:us-east-1:1:secret:vault/wallet/x/share-b"
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsCreated(w, arn)
	}), "").(*WalletStore)

	gotARN, err := store.Create(context.Background(), "vault/wallet/x/share-b", want)
	if err != nil {
		t.Fatal("create failed")
	}
	if gotARN != arn {
		t.Fatal("ARN changed")
	}
	if got.target != "secretsmanager.CreateSecret" || jsonString(t, got.body, "Name") != "vault/wallet/x/share-b" {
		t.Fatal("secret name changed")
	}
	if !bytes.Equal(secretBinary(t, got.body), want) {
		t.Fatal("secret bytes changed")
	}
	for _, extra := range []string{"Description", "SecretString", "KmsKeyId", "Tags"} {
		if _, ok := got.body[extra]; ok {
			t.Fatalf("CreateSecret gained %s", extra)
		}
	}
}

func TestCreate_Forwards_TheNameUnchanged(t *testing.T) {
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsCreated(w, "arn:aws:secretsmanager:us-east-1:1:secret:padded")
	}), "").(*WalletStore)
	if _, err := store.Create(context.Background(), "  vault/wallet/x/share-b  ", []byte{0x01}); err != nil {
		t.Fatal("create failed")
	}
	if jsonString(t, got.body, "Name") != "  vault/wallet/x/share-b  " {
		t.Fatal("secret name changed")
	}
}

func TestCreate_Forwards_TheAPIError(t *testing.T) {
	store := NewWalletStore(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		writeAWSError(w, "application/x-amz-json-1.1", "boom")
	}), "").(*WalletStore)
	_, err := store.Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal("API error was not returned unchanged")
	}
}

func TestCreate_Returns_AnEmptyARNWhenTheResponseHasNone(t *testing.T) {
	store := NewWalletStore(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte("{}"))
	}), "").(*WalletStore)
	arn, err := store.Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01})
	if err != nil {
		t.Fatal("create failed")
	}
	if arn != "" {
		t.Fatal("missing ARN was not returned empty")
	}
}

func TestWallet_Store_BinaryReadsTheGivenSecretAndReturnsItsBytes(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03, 0x04}
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsValue(w, want)
	}), "").(*WalletStore)

	binary, err := store.Binary(context.Background(), "vault/wallet/x/share-b")
	if err != nil {
		t.Fatal("read failed")
	}
	if got.target != "secretsmanager.GetSecretValue" || jsonString(t, got.body, "SecretId") != "vault/wallet/x/share-b" {
		t.Fatal("secret id changed")
	}
	if _, ok := got.body["VersionId"]; ok {
		t.Fatal("GetSecretValue gained VersionId")
	}
	if _, ok := got.body["VersionStage"]; ok {
		t.Fatal("GetSecretValue gained VersionStage")
	}
	if !bytes.Equal(binary, want) {
		t.Fatal("secret bytes changed")
	}
}

func TestWallet_Store_BinaryForwardsTheSecretIDUnchanged(t *testing.T) {
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsValue(w, []byte{0x01})
	}), "").(*WalletStore)
	if _, err := store.Binary(context.Background(), "  vault/wallet/x/share-b  "); err != nil {
		t.Fatal("read failed")
	}
	if jsonString(t, got.body, "SecretId") != "  vault/wallet/x/share-b  " {
		t.Fatal("secret id changed")
	}
}

func TestWallet_Store_BinaryForwardsTheAPIError(t *testing.T) {
	store := NewWalletStore(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		writeAWSError(w, "application/x-amz-json-1.1", "boom")
	}), "").(*WalletStore)
	_, err := store.Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal("API error was not returned unchanged")
	}
}

func writeSecretsCreated(w http.ResponseWriter, arn string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(map[string]string{"ARN": arn})
}

func secretBinary(t *testing.T, body map[string]json.RawMessage) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(jsonString(t, body, "SecretBinary"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCreate_Seals_TheShareWithTheMasterKey(t *testing.T) {
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsCreated(w, "arn:aws:secretsmanager:us-east-1:1:secret:vault/wallet/x/share-b")
	}), "alias/waas-dev-master").(*WalletStore)

	if _, err := store.Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01}); err != nil {
		t.Fatal("create failed")
	}
	if jsonString(t, got.body, "KmsKeyId") != "alias/waas-dev-master" {
		t.Fatal("the share was not sealed with the master key")
	}
}

func TestLookup_Returns_TheARNOfAName(t *testing.T) {
	const arn = "arn:aws:secretsmanager:us-east-1:1:secret:vault/wallet/x/share-b-AbCdEf"
	var got awsRequest
	store := NewWalletStore(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsCreated(w, arn)
	}), "").(*WalletStore)

	gotARN, found, err := store.Lookup(context.Background(), "vault/wallet/x/share-b")
	if err != nil || !found || gotARN != arn {
		t.Fatalf("Lookup = %q, %v, %v; want the ARN", gotARN, found, err)
	}
	if got.target != "secretsmanager.DescribeSecret" || jsonString(t, got.body, "SecretId") != "vault/wallet/x/share-b" {
		t.Fatal("DescribeSecret was not asked for the name")
	}
}

func TestLookup_Reports_AMissingSecretWithoutAnError(t *testing.T) {
	store := NewWalletStore(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		writeAWSError(w, "application/x-amz-json-1.1", "Secrets Manager can't find the specified secret.")
	}), "").(*WalletStore)

	arn, found, err := store.Lookup(context.Background(), "vault/wallet/x/share-b")
	if err != nil || found || arn != "" {
		t.Fatalf("Lookup = %q, %v, %v; want not found and no error", arn, found, err)
	}
}

func TestLookup_Forwards_AnyOtherAPIError(t *testing.T) {
	store := NewWalletStore(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": "AccessDeniedException", "message": "denied"})
	}), "").(*WalletStore)

	if _, found, err := store.Lookup(context.Background(), "vault/wallet/x/share-b"); err == nil || found {
		t.Fatal("an access error was taken for a missing secret")
	}
}

func TestNew_Share_StoreWithoutAClientReportsIt(t *testing.T) {
	store := NewShareStore(nil, "alias/waas-dev-master")
	if _, _, err := store.Lookup(context.Background(), "vault/wallet/x/share-b"); err == nil {
		t.Fatal("expected error for a nil client")
	}
	if _, err := store.Create(context.Background(), "vault/wallet/x/share-b", []byte{0x01}); err == nil {
		t.Fatal("expected error for a nil client")
	}
}
