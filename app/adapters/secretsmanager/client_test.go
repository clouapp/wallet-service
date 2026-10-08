package secretsmanager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
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
	client := &Client{api: secretsClient(t, func(awsRequest, http.ResponseWriter) {
		t.Fatal("validation must not call Secrets Manager")
	})}
	if _, err := client.Binary(nil, "vault/wallet/x/share-b"); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if _, err := client.Binary(context.Background(), ""); err == nil {
		t.Fatal("expected error for an empty secret id")
	}
}

func TestBinary_Reads_TheGivenSecretAndReturnsItsBytes(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03, 0x04}
	var got awsRequest
	client := New(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsValue(w, want)
	})).(*Client)

	binary, err := client.Binary(context.Background(), "vault/wallet/x/share-b")
	if err != nil {
		t.Fatal("read failed")
	}
	if got.target != "secretsmanager.GetSecretValue" || got.method != http.MethodPost || got.path != "/" {
		t.Fatalf("request %s %s target %s", got.method, got.path, got.target)
	}
	if !strings.HasPrefix(got.auth, "AWS4-HMAC-SHA256") || !strings.Contains(got.contentType, "application/x-amz-json-1.1") {
		t.Fatalf("auth %q content-type %q", got.auth, got.contentType)
	}
	if jsonString(t, got.body, "SecretId") != "vault/wallet/x/share-b" {
		t.Fatal("secret id changed")
	}
	if _, ok := got.body["VersionId"]; ok {
		t.Fatal("GetSecretValue gained VersionId")
	}
	if _, ok := got.body["VersionStage"]; ok {
		t.Fatal("GetSecretValue gained VersionStage")
	}
	if string(binary) != string(want) {
		t.Fatal("secret bytes changed")
	}
}

func TestBinary_Forwards_TheAPIError(t *testing.T) {
	client := New(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		writeAWSError(w, "application/x-amz-json-1.1", "boom")
	})).(*Client)
	_, err := client.Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal("API error was not returned unchanged")
	}
}

func TestBinary_Rejects_AnEmptyResponse(t *testing.T) {
	client := New(secretsClient(t, func(_ awsRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
	})).(*Client)
	_, err := client.Binary(context.Background(), "vault/wallet/x/share-b")
	if err == nil {
		t.Fatal("expected error for an empty response")
	}
}

func TestBinary_Forwards_TheSecretIDUnchanged(t *testing.T) {
	var got awsRequest
	client := New(secretsClient(t, func(call awsRequest, w http.ResponseWriter) {
		got = call
		writeSecretsValue(w, []byte{0x01})
	})).(*Client)
	if _, err := client.Binary(context.Background(), "  vault/wallet/x/share-b  "); err != nil {
		t.Fatal("read failed")
	}
	if jsonString(t, got.body, "SecretId") != "  vault/wallet/x/share-b  " {
		t.Fatal("secret id changed")
	}
}

type awsRequest struct {
	method      string
	path        string
	target      string
	auth        string
	contentType string
	body        map[string]json.RawMessage
}

func secretsClient(t *testing.T, respond func(awsRequest, http.ResponseWriter)) *awssm.Client {
	t.Helper()
	return awsJSONClient(t, func(cfg aws.Config, endpoint string) any {
		return awssm.NewFromConfig(cfg, func(o *awssm.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), 1)
		})
	}, respond).(*awssm.Client)
}

func awsJSONClient(t *testing.T, build func(aws.Config, string) any, respond func(awsRequest, http.ResponseWriter)) any {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body := map[string]json.RawMessage{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("body: %v", err)
			}
		}
		respond(awsRequest{
			method:      r.Method,
			path:        r.URL.Path,
			target:      r.Header.Get("X-Amz-Target"),
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}, w)
	}))
	t.Cleanup(server.Close)
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}
	return build(cfg, server.URL)
}

func writeSecretsValue(w http.ResponseWriter, binary []byte) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"ARN":          "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/x/share-b",
		"Name":         "vault/wallet/x/share-b",
		"SecretBinary": base64.StdEncoding.EncodeToString(binary),
	})
}

func writeAWSError(w http.ResponseWriter, contentType, message string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Amzn-ErrorType", "ResourceNotFoundException")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  "ResourceNotFoundException",
		"message": message,
	})
}

func jsonString(t *testing.T, body map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := body[key]
	if !ok {
		t.Fatalf("missing %s", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
