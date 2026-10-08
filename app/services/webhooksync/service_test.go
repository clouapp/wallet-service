package webhooksync

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

const (
	syncOpenedKey = "ing-opened-a91c"
	syncNextKey   = "ing-opened-b27e"
	syncEnvKey    = "ing-env-44d0"
	syncSigning   = "ing-sign-subscription"
)

func TestSync_ChainAddresses_ReadsTheProviderKeyOnEverySync(t *testing.T) {
	logs := captureSyncLogs(t)
	rows := &syncSettingsRows{groups: map[string]map[string]string{
		"provider_alchemy": {
			"enabled":    "true",
			"auth_token": "enc:v1:" + syncOpenedKey,
		},
	}}
	accountSettings := settings.NewService(settings.Deps{Store: rows, Sealer: syncPrefixSealer{}, Cache: nil, Activity: syncDiscardActivity{}})
	stub := &syncStub{}
	var seen []string
	service := NewService(Deps{
		Subscriptions: syncSubs{},
		Addresses:     syncAddresses{},
		Providers: map[string]AddressSyncer{
			"alchemy": stub,
		},
		ProviderKey: func(ctx context.Context, provider string) string {
			opened := accountSettings.IngestProviderKey(ctx, provider, syncEnvKey)
			seen = append(seen, opened)
			return opened
		},
	})
	service.openSecret = func(string) (string, error) { return syncSigning, nil }

	if err := service.SyncChainAddresses(context.Background(), "eth"); err != nil {
		t.Fatal("the first sync failed")
	}
	rows.groups["provider_alchemy"]["auth_token"] = "enc:v1:" + syncNextKey
	if err := service.SyncChainAddresses(context.Background(), "eth"); err != nil {
		t.Fatal("the second sync failed")
	}
	if len(seen) != 2 || seen[0] != syncOpenedKey || seen[1] != syncNextKey || stub.calls != 2 {
		t.Fatal("the second sync reused the key opened for the first")
	}
	requireSyncLogsOmit(t, logs.String(), syncOpenedKey, syncNextKey, syncEnvKey, syncSigning, "enc:v1:")
}

func TestSync_ChainAddresses_EmptyKeyDoesNotCallTheProvider(t *testing.T) {
	logs := captureSyncLogs(t)
	stub := &syncStub{}
	service := NewService(Deps{
		Subscriptions: syncSubs{},
		Addresses:     syncAddresses{},
		Providers: map[string]AddressSyncer{
			"alchemy": stub,
		},
		ProviderKey: func(context.Context, string) string { return "" },
	})
	service.openSecret = func(string) (string, error) { return syncSigning, nil }

	err := service.SyncChainAddresses(context.Background(), "eth")
	if err == nil || stub.calls != 0 {
		t.Fatal("an empty provider key still called the provider")
	}
	if strings.Contains(err.Error(), syncSigning) {
		t.Fatal("the sync error carried a credential")
	}
	requireSyncLogsOmit(t, logs.String(), syncOpenedKey, syncEnvKey, syncSigning, "enc:v1:")
}

type syncSubs struct{}

func (syncSubs) FindByChainID(context.Context, string) (*models.WebhookSubscription, error) {
	return &models.WebhookSubscription{
		ID:                uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ChainID:           "eth",
		Provider:          "alchemy",
		ProviderWebhookID: "wh-1",
		SigningSecret:     "enc:v1:" + syncSigning,
	}, nil
}

func (syncSubs) FindAllActive(context.Context) ([]models.WebhookSubscription, error) {
	return nil, nil
}

func (syncSubs) SetSyncStatus(context.Context, uuid.UUID, string) error { return nil }

func (syncSubs) RecordSync(context.Context, uuid.UUID, string, string, time.Time) error {
	return nil
}

type syncAddresses struct{}

func (syncAddresses) PluckActiveAddresses(context.Context, string) ([]string, error) {
	return []string{"0xabc"}, nil
}

type syncStub struct {
	calls int
}

func (s *syncStub) SyncAddresses(context.Context, string, []string) error {
	s.calls++
	return nil
}

type syncSettingsRows struct {
	groups map[string]map[string]string
}

func (s *syncSettingsRows) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, nil
}

func (s *syncSettingsRows) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return nil
}

func (s *syncSettingsRows) ListPlatform(_ context.Context, group string) ([]models.Setting, error) {
	values := s.groups[group]
	rows := make([]models.Setting, 0, len(values))
	for key, value := range values {
		rows = append(rows, models.Setting{Group: group, Key: key, Value: value})
	}
	return rows, nil
}

type syncPrefixSealer struct{}

func (syncPrefixSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + plaintext, nil
}

func (syncPrefixSealer) Open(value string) (string, error) {
	raw, ok := strings.CutPrefix(value, "enc:v1:")
	if !ok {
		return "", errors.New("open refused")
	}
	return raw, nil
}

type syncDiscardActivity struct{}

func (syncDiscardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (syncDiscardActivity) Append(context.Context, models.AccountActivity) error { return nil }

func captureSyncLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireSyncLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a credential appeared in a log line")
		}
	}
}
