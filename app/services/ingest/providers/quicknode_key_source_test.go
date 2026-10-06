package providers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/ingest/providers"
)

func TestVerifyInbound_EmptyResolvedQuickNodeKeyRejectsTheSignature(t *testing.T) {
	logs := captureAlchemyIngestLogs(t)
	provider := inboundKeyPort{source: func(context.Context) string { return "" }}
	headers := providers.Header{}
	headers.Set("X-QN-Signature", "ignored-by-the-port")
	valid, err := provider.VerifyInbound(headers, []byte(`[{"txid":"x"}]`), verifySigning)
	if err == nil || valid {
		t.Fatal("an empty resolved key accepted the webhook")
	}
	if strings.Contains(err.Error(), verifyBootKey) || strings.Contains(err.Error(), verifySigning) {
		t.Fatal("the verify error carried a credential")
	}
	requireAlchemyIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}
