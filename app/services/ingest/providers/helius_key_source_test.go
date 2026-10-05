package providers_test

import (
	"context"
	"strings"
	"testing"

	heliusingest "github.com/macrowallets/waas/app/adapters/ingest/helius"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

func TestVerifyInbound_EmptyResolvedHeliusKeyRejectsTheSignature(t *testing.T) {
	logs := captureAlchemyIngestLogs(t)
	body := []byte(`[{"signature":"abc"}]`)
	provider := heliusingest.NewHeliusProvider(verifyBootKey).UseKeySource(func(context.Context) string { return "" })
	headers := providers.Header{}
	headers.Set("Authorization", verifySigning)
	valid, err := provider.VerifyInbound(headers, body, verifySigning)
	if err == nil || valid {
		t.Fatal("an empty resolved key accepted the webhook")
	}
	if strings.Contains(err.Error(), verifyBootKey) || strings.Contains(err.Error(), verifySigning) {
		t.Fatal("the verify error carried a credential")
	}
	requireAlchemyIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}
