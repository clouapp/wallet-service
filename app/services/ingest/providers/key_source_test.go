package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
)

func computeAlchemySignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

const (
	verifyOpenedKey = "ing-opened-a91c"
	verifyEnvKey    = "ing-env-44d0"
	verifyBootKey   = "ing-boot-must-not-stick"
	verifySigning   = "ing-sign-subscription"
)

func TestVerifyInbound_EmptyResolvedKeyRejectsTheSignature(t *testing.T) {
	logs := captureIngestLogs(t)
	body := []byte(`{"event":"test"}`)

	cases := []struct {
		name    string
		provide func(KeySource) WebhookProvider
		sign    func(Header)
	}{
		{
			name: "quicknode",
			provide: func(source KeySource) WebhookProvider {
				return NewQuickNodeProvider(verifyBootKey).UseKeySource(source)
			},
			sign: func(headers Header) {
				headers.Set(quicknodeDefaultSignatureHeader, computeAlchemySignature(body, verifySigning))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := tc.provide(func(context.Context) string { return "" })
			headers := Header{}
			tc.sign(headers)
			valid, err := provider.VerifyInbound(headers, body, verifySigning)
			if err == nil || valid {
				t.Fatal("an empty resolved key accepted the webhook")
			}
			if strings.Contains(err.Error(), verifyBootKey) || strings.Contains(err.Error(), verifySigning) {
				t.Fatal("the verify error carried a credential")
			}
		})
	}
	requireIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}

func captureIngestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireIngestLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a credential appeared in a log line")
		}
	}
}
