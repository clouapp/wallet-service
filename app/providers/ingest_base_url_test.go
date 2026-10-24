package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractslog "github.com/goravel/framework/contracts/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingestsubscription"
	"github.com/macrowallets/waas/app/services/security"
	"github.com/macrowallets/waas/app/services/settings"
)

// stringConfig is a configuration a test edits between two reads.
type stringConfig map[string]string

func (c stringConfig) GetString(path string, defaultValue ...string) string {
	if value, ok := c[path]; ok {
		return value
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

// platformRows is a settings store holding platform groups only; err fails every read.
type platformRows struct {
	groups map[string]map[string]string
	err    error
}

func (p *platformRows) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, p.err
}

func (p *platformRows) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return p.err
}

func (p *platformRows) ListPlatform(_ context.Context, group string) ([]models.Setting, error) {
	if p.err != nil {
		return nil, p.err
	}
	var rows []models.Setting
	for key, value := range p.groups[group] {
		rows = append(rows, models.Setting{Group: group, Key: key, Value: value})
	}
	return rows, nil
}

type plainSealer struct{}

func (plainSealer) Seal(plaintext string) (string, error) { return "enc:v1:" + plaintext, nil }

func (plainSealer) Open(value string) (string, error) { return value, nil }

type noActivity struct{}

func (noActivity) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func (noActivity) Append(context.Context, models.AccountActivity) error { return nil }

func ingestBaseURLSettings(store *platformRows) *settings.Service {
	return settings.NewService(settings.Deps{Store: store, Sealer: plainSealer{}, Activity: noActivity{}})
}

func TestIngest_BaseURL_ReadsTheSettingThenTheEnvironmentOnEveryCall(t *testing.T) {
	store := &platformRows{groups: map[string]map[string]string{}}
	cfg := stringConfig{ingestBaseURLConfigKey: "https://env.wallets.example.com"}
	read := ingestBaseURL(ingestBaseURLSettings(store), cfg)
	ctx := context.Background()
	readOK := func() ingestsubscription.BaseURL {
		t.Helper()
		got, err := read(ctx)
		require.NoError(t, err)
		return got
	}

	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, readOK(),
		"no setting: WEBHOOK_INGEST_BASE_URL")

	store.groups["ingest_push"] = map[string]string{"base_url": "https://push.wallets.example.com"}
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://push.wallets.example.com", Source: ingestsubscription.BaseURLFromSetting}, readOK(),
		"a setting stored after the reader was built applies to the next call")

	store.groups["ingest_push"] = map[string]string{"base_url": "https://169.254.169.254."}
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv, SettingInvalid: true}, readOK(),
		"an invalid setting gives the base URL back to the environment, marked")

	store.groups["ingest_push"] = map[string]string{"base_url": ""}
	cfg[ingestBaseURLConfigKey] = "https://env2.wallets.example.com"
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env2.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, readOK(),
		"the environment value is read at the call as well")
}

func TestIngest_BaseURL_AFailedSettingsReadIsAnErrorBesideTheEnvironmentValue(t *testing.T) {
	store := &platformRows{groups: map[string]map[string]string{"ingest_push": {"base_url": "https://push.wallets.example.com"}}}
	read := ingestBaseURL(ingestBaseURLSettings(store), stringConfig{ingestBaseURLConfigKey: "https://env.wallets.example.com"})
	store.err = errors.New("connection refused")

	got, err := read(context.Background())

	require.ErrorIs(t, err, settings.ErrIngestBaseURLUnread)
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, got)
}

func TestIngest_BaseURL_NothingConfiguredIsNoURL(t *testing.T) {
	read := ingestBaseURL(ingestBaseURLSettings(&platformRows{}), stringConfig{})

	got, err := read(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got.URL)
	assert.Equal(t, ingestsubscription.BaseURLFromEnv, got.Source)
}

// The fallback warnings name the group and the reason: neither the stored URL nor
// the environment one reaches the log, through the redacting channel every process
// logs through.
func TestIngest_BaseURL_FallbackWarningsNeverCarryAURL(t *testing.T) {
	preserveDefaultSlog(t)
	t.Cleanup(func() { security.ConfigureRedaction(nil, nil) })
	inner := &stubHandler{}
	InstallLogRedaction(&mapConfig{data: map[string]any{
		"logging": map[string]any{
			"default":  "app",
			"channels": map[string]any{"app": map[string]any{"driver": contractslog.DriverCustom, "via": stubLogger{handler: inner}}},
		},
	}}, nil)
	const (
		storedURL = "https://stored-ingest-value.wallets.example.com"
		envURL    = "https://env-ingest-value.wallets.example.com"
	)
	invalidStored := "https://169.254.169.254./stored-ingest-path"
	// A control line proves the capture would show a URL: the redaction keeps it.
	slog.Info("capture control", "url", storedURL)
	require.Len(t, inner.got, 1)
	require.Contains(t, fmt.Sprint(inner.with), storedURL, "the capture redacts a plain URL, so it cannot prove the warnings leave it out")
	inner.got, inner.with = nil, nil

	store := &platformRows{groups: map[string]map[string]string{"ingest_push": {"base_url": invalidStored}}}
	read := ingestBaseURL(ingestBaseURLSettings(store), stringConfig{ingestBaseURLConfigKey: envURL})
	_, err := read(context.Background())
	require.NoError(t, err)
	store.groups["ingest_push"] = map[string]string{"base_url": storedURL}
	store.err = errors.New("connection refused")
	_, err = read(context.Background())
	require.Error(t, err)

	require.Len(t, inner.got, 2, "one warning for the invalid setting, one for the failed read")
	logged := strings.Join(inner.got, "\n") + " " + fmt.Sprint(inner.with)
	assert.Contains(t, logged, "ingest_push")
	for _, value := range []string{storedURL, envURL, invalidStored, "stored-ingest", "env-ingest", "169.254.169.254"} {
		assert.NotContains(t, logged, value)
	}
}
