package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingestsubscription"
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

	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, read(ctx),
		"no setting: WEBHOOK_INGEST_BASE_URL")

	store.groups["ingest_push"] = map[string]string{"base_url": "https://push.wallets.example.com"}
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://push.wallets.example.com", Source: ingestsubscription.BaseURLFromSetting}, read(ctx),
		"a setting stored after the reader was built applies to the next call")

	store.groups["ingest_push"] = map[string]string{"base_url": "https://169.254.169.254"}
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, read(ctx),
		"an invalid setting gives the base URL back to the environment")

	store.groups["ingest_push"] = map[string]string{"base_url": ""}
	cfg[ingestBaseURLConfigKey] = "https://env2.wallets.example.com"
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env2.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, read(ctx),
		"the environment value is read at the call as well")

	store.groups["ingest_push"] = map[string]string{"base_url": "https://push.wallets.example.com"}
	store.err = errors.New("connection refused")
	assert.Equal(t, ingestsubscription.BaseURL{URL: "https://env2.wallets.example.com", Source: ingestsubscription.BaseURLFromEnv}, read(ctx),
		"a failed settings read keeps the environment value")
}

func TestIngest_BaseURL_NothingConfiguredIsNoURL(t *testing.T) {
	read := ingestBaseURL(ingestBaseURLSettings(&platformRows{}), stringConfig{})

	got := read(context.Background())

	assert.Empty(t, got.URL)
	assert.Equal(t, ingestsubscription.BaseURLFromEnv, got.Source)
}
