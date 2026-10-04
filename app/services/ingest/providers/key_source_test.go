package providers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

const (
	verifyOpenedKey = "ing-opened-a91c"
	verifyEnvKey    = "ing-env-44d0"
	verifyBootKey   = "ing-boot-must-not-stick"
	verifySigning   = "ing-sign-subscription"
)

func TestVerifyInbound_EnabledGroupSuppliesTheOpenedKey(t *testing.T) {
	logs := captureIngestLogs(t)
	rows := settings.NewService(&ingestSettingsRows{groups: map[string]map[string]string{
		"provider_alchemy": {
			"enabled":    "true",
			"auth_token": "enc:v1:" + verifyOpenedKey,
		},
	}}, ingestPrefixSealer{}, nil, ingestDiscardActivity{})
	var seen string
	provider := NewAlchemyProvider(verifyBootKey).UseKeySource(func(ctx context.Context) string {
		seen = rows.IngestProviderKey(ctx, "alchemy", verifyEnvKey)
		return seen
	})

	body := []byte(`{"event":"test"}`)
	headers := Header{}
	headers.Set("X-Alchemy-Signature", computeAlchemySignature(body, verifySigning))
	valid, err := provider.VerifyInbound(headers, body, verifySigning)
	if err != nil || !valid {
		t.Fatal("a webhook signed with the subscription secret was rejected")
	}
	if seen != verifyOpenedKey || seen == verifyBootKey || seen == verifyEnvKey {
		t.Fatal("an enabled group did not supply the opened key at verify time")
	}
	requireIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, verifySigning, "enc:v1:")
}

func TestVerifyInbound_EmptyResolvedKeyRejectsTheSignature(t *testing.T) {
	logs := captureIngestLogs(t)
	body := []byte(`{"event":"test"}`)
	signature := computeAlchemySignature(body, verifySigning)

	cases := []struct {
		name    string
		provide func(KeySource) WebhookProvider
		sign    func(Header)
	}{
		{
			name: "alchemy",
			provide: func(source KeySource) WebhookProvider {
				return NewAlchemyProvider(verifyBootKey).UseKeySource(source)
			},
			sign: func(headers Header) {
				headers.Set("X-Alchemy-Signature", signature)
			},
		},
		{
			name: "helius",
			provide: func(source KeySource) WebhookProvider {
				return NewHeliusProvider(verifyBootKey).UseKeySource(source)
			},
			sign: func(headers Header) {
				headers.Set("Authorization", verifySigning)
			},
		},
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

func TestAlchemyHeaders_ReadTheKeySourceOnEveryCall(t *testing.T) {
	logs := captureIngestLogs(t)
	n := 0
	provider := NewAlchemyProvider(verifyBootKey).UseKeySource(func(context.Context) string {
		n++
		if n == 1 {
			return verifyOpenedKey
		}
		return verifyEnvKey
	})

	first, err := provider.apiHeaders(context.Background())
	if err != nil || first[alchemyAuthTokenHdr] != verifyOpenedKey {
		t.Fatal("the first call did not use the key source")
	}
	second, err := provider.apiHeaders(context.Background())
	if err != nil || second[alchemyAuthTokenHdr] != verifyEnvKey || n != 2 {
		t.Fatal("the second call reused the first key")
	}
	requireIngestLogsOmit(t, logs.String(), verifyOpenedKey, verifyEnvKey, verifyBootKey, "enc:v1:")
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
