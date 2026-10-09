package secretsmanager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func shareBClient(t *testing.T, status int, message string, binary []byte, asked *string) *awssm.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			t.Errorf("request %s target %s", r.Method, r.Header.Get("X-Amz-Target"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		if asked != nil {
			*asked = body["SecretId"]
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if status != http.StatusOK {
			w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "AccessDeniedException", "message": message})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"SecretBinary": base64.StdEncoding.EncodeToString(binary),
		})
	}))
	t.Cleanup(server.Close)
	return awssm.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}, func(o *awssm.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), 1)
	})
}

func TestShare_B_FetchesTheWalletSecretWithoutLeakingErrors(t *testing.T) {
	wallet := models.Wallet{ID: uuid.MustParse("01e5e921-9443-4718-bf60-d624e65b03d9"), MPCSecretARN: "arn:aws:secretsmanager:test"}
	var asked string
	secrets := NewShareB(shareBClient(t, http.StatusOK, "", []byte(`{"Xi":1}`), &asked))
	share, err := secrets.FetchShareB(context.Background(), wallet)
	if err != nil || string(share) != `{"Xi":1}` || asked != wallet.MPCSecretARN {
		t.Fatalf("share %q err %v asked %s", share, err, asked)
	}
	failing := NewShareB(shareBClient(t, http.StatusBadRequest, "AccessDenied: secret value sk-123", nil, nil))
	if _, err := failing.FetchShareB(context.Background(), wallet); err == nil || strings.Contains(err.Error(), "sk-123") {
		t.Fatalf("errors must not echo the provider message, got %v", err)
	}
	wallet.MPCSecretARN = " "
	if _, err := secrets.FetchShareB(context.Background(), wallet); err == nil {
		t.Fatal("a wallet without an ARN must be refused")
	}
	if _, err := NewShareB(nil).FetchShareB(context.Background(), wallet); err == nil {
		t.Fatal("a missing secrets manager must be refused")
	}
}
