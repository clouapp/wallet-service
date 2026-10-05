package providers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	quicknodeingest "github.com/macrowallets/waas/app/adapters/ingest/quicknode"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

func TestVerifyInbound_EmptyResolvedQuickNodeKeyRejectsTheSignature(t *testing.T) {
	logs := captureAlchemyIngestLogs(t)
	body := []byte(`[{"txid":"x"}]`)
	provider := quicknodeingest.NewQuickNodeProvider(verifyBootKey).UseKeySource(func(context.Context) string { return "" })
	mac := hmac.New(sha256.New, []byte(verifySigning))
	mac.Write(body)
	headers := providers.Header{}
	headers.Set("X-QN-Signature", hex.EncodeToString(mac.Sum(nil)))
	valid, err := provider.VerifyInbound(headers, body, verifySigning)
	if err == nil || valid {
		t.Fatal("an empty resolved key accepted the webhook")
	}
	if strings.Contains(err.Error(), verifyBootKey) || strings.Contains(err.Error(), verifySigning) {
		t.Fatal("the verify error carried a credential")
	}
	requireAlchemyIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}
