package providers_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/settings"
)

const (
	verifyOpenedKey = "ing-opened-a91c"
	verifyEnvKey    = "ing-env-44d0"
	verifyBootKey   = "ing-boot-must-not-stick"
	verifySigning   = "ing-sign-subscription"
)

func TestVerifyInbound_EnabledGroupSuppliesTheOpenedKey(t *testing.T) {
	logs := captureAlchemyIngestLogs(t)
	rows := settings.NewService(settings.Deps{Store: &ingestSettingsRows{groups: map[string]map[string]string{
		"provider_alchemy": {
			"enabled":    "true",
			"auth_token": "enc:v1:" + verifyOpenedKey,
		},
	}}, Sealer: ingestPrefixSealer{}, Cache: nil, Activity: ingestDiscardActivity{}})
	var seen string
	provider := inboundKeyPort{source: func(ctx context.Context) string {
		seen = rows.IngestProviderKey(ctx, "alchemy", verifyEnvKey)
		return seen
	}}

	body := []byte(`{"event":"test"}`)
	headers := providers.Header{}
	headers.Set("X-Alchemy-Signature", openedAlchemySignature(body, verifySigning))
	valid, err := provider.VerifyInbound(headers, body, verifySigning)
	if err != nil || !valid {
		t.Fatal("a webhook signed with the subscription secret was rejected")
	}
	if seen != verifyOpenedKey || seen == verifyBootKey || seen == verifyEnvKey {
		t.Fatal("an enabled group did not supply the opened key at verify time")
	}
	requireAlchemyIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}

func TestVerifyInbound_EmptyResolvedAlchemyKeyRejectsTheSignature(t *testing.T) {
	logs := captureAlchemyIngestLogs(t)
	body := []byte(`{"event":"test"}`)
	provider := inboundKeyPort{source: func(context.Context) string { return "" }}
	headers := providers.Header{}
	headers.Set("X-Alchemy-Signature", openedAlchemySignature(body, verifySigning))
	valid, err := provider.VerifyInbound(headers, body, verifySigning)
	if err == nil || valid {
		t.Fatal("an empty resolved key accepted the webhook")
	}
	if strings.Contains(err.Error(), verifyBootKey) || strings.Contains(err.Error(), verifySigning) {
		t.Fatal("the verify error carried a credential")
	}
	requireAlchemyIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}

// inboundKeyPort is the ingest port. It consults the key source and refuses a
// blank credential; signature bytes stay with the provider adapter.
type inboundKeyPort struct {
	source providers.KeySource
}

func (p inboundKeyPort) VerifyInbound(providers.Header, []byte, string) (bool, error) {
	if err := providers.GateInboundCredential(context.Background(), p.source); err != nil {
		return false, err
	}
	return true, nil
}

func openedAlchemySignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

type ingestSettingsRows struct {
	groups map[string]map[string]string
}

func (s *ingestSettingsRows) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, nil
}

func (s *ingestSettingsRows) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return nil
}

func (s *ingestSettingsRows) ListPlatform(_ context.Context, group string) ([]models.Setting, error) {
	values := s.groups[group]
	rows := make([]models.Setting, 0, len(values))
	for key, value := range values {
		rows = append(rows, models.Setting{Group: group, Key: key, Value: value})
	}
	return rows, nil
}

type ingestPrefixSealer struct{}

func (ingestPrefixSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + plaintext, nil
}

func (ingestPrefixSealer) Open(value string) (string, error) {
	raw, ok := strings.CutPrefix(value, "enc:v1:")
	if !ok {
		return "", errors.New("open refused")
	}
	return raw, nil
}

type ingestDiscardActivity struct{}

func (ingestDiscardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (ingestDiscardActivity) Append(context.Context, models.AccountActivity) error { return nil }

func captureAlchemyIngestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireAlchemyIngestLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a credential appeared in a log line")
		}
	}
}
