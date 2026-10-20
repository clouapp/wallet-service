package ingestsubscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

const (
	testBaseURL     = "https://wallets.example.com"
	testProviderKey = "alchemy-key-7f3a"
	testSecret      = "whsec-signing-91bd"
)

type memoryStore struct {
	rows      []models.WebhookSubscription
	createErr error
}

func (m *memoryStore) FindByProviderAndChain(_ context.Context, provider, chainID string) (*models.WebhookSubscription, error) {
	for i := range m.rows {
		if m.rows[i].Provider == provider && m.rows[i].ChainID == chainID {
			return &m.rows[i], nil
		}
	}
	return nil, models.ErrRepositoryNotFound
}

func (m *memoryStore) FindByChainID(_ context.Context, chainID string) (*models.WebhookSubscription, error) {
	for i := range m.rows {
		if m.rows[i].ChainID == chainID && m.rows[i].Status == models.WebhookSubscriptionActive {
			return &m.rows[i], nil
		}
	}
	return nil, models.ErrRepositoryNotFound
}

func (m *memoryStore) FindAll(context.Context) ([]models.WebhookSubscription, error) {
	return append([]models.WebhookSubscription(nil), m.rows...), nil
}

func (m *memoryStore) Create(_ context.Context, sub *models.WebhookSubscription) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.rows = append(m.rows, *sub)
	return nil
}

type fakeCreator struct {
	configs   []providers.ProviderConfig
	webhook   *providers.ProviderWebhook
	createErr error
	deleted   []string
	deleteErr error
}

func (f *fakeCreator) CreateWebhook(_ context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error) {
	f.configs = append(f.configs, cfg)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.webhook, nil
}

func (f *fakeCreator) DeleteWebhook(_ context.Context, webhookID string) error {
	f.deleted = append(f.deleted, webhookID)
	return f.deleteErr
}

type fakeSync struct {
	chains []string
	err    error
}

func (f *fakeSync) SyncChainAddresses(_ context.Context, chainID string) error {
	f.chains = append(f.chains, chainID)
	return f.err
}

type fixedAddresses []string

func (a fixedAddresses) PluckActiveAddresses(context.Context, string) ([]string, error) {
	return a, nil
}

type fixture struct {
	svc     *Service
	store   *memoryStore
	creator *fakeCreator
	sync    *fakeSync
	keys    map[string]string
}

func newFixture(t *testing.T, mutate func(*Deps)) *fixture {
	t.Helper()
	f := &fixture{
		store:   &memoryStore{},
		creator: &fakeCreator{webhook: &providers.ProviderWebhook{ProviderWebhookID: "wh_123", SigningSecret: testSecret}},
		sync:    &fakeSync{},
		keys:    map[string]string{providers.ProviderAlchemy: testProviderKey},
	}
	deps := Deps{
		Subscriptions: f.store,
		Addresses:     fixedAddresses{"0xalice", "0xbob"},
		Sync:          f.sync,
		Creators:      map[string]Creator{providers.ProviderAlchemy: f.creator, providers.ProviderHelius: f.creator},
		ProviderKey:   func(_ context.Context, provider string) string { return f.keys[provider] },
		ChainNetwork: func(_ context.Context, chainID string) (string, error) {
			switch chainID {
			case "teth":
				return models.NetworkEthereumSepolia, nil
			case "tsol":
				return models.NetworkSolanaDevnet, nil
			case "ttron":
				return models.NetworkTronNile, nil
			}
			return "", fmt.Errorf("chain %s: %w", chainID, models.ErrRepositoryNotFound)
		},
		BaseURL: testBaseURL,
		Seal:    func(plaintext string) (string, error) { return "enc:v1:sealed(" + plaintext + ")", nil },
	}
	if mutate != nil {
		mutate(&deps)
	}
	f.svc = NewService(deps)
	return f
}

func subscribe(f *fixture, provider, chainID string, apply bool) (Plan, error) {
	return f.svc.Subscribe(context.Background(), Request{Provider: provider, ChainID: chainID, Apply: apply})
}

// requireNoCredential fails when a credential shows in text.
func requireNoCredential(t *testing.T, text string) {
	t.Helper()
	for _, credential := range []string{testProviderKey, testSecret} {
		require.NotContains(t, text, credential)
	}
}

func TestSubscribe_DryRun_PlansWithoutCallingTheProvider(t *testing.T) {
	f := newFixture(t, nil)

	plan, err := subscribe(f, "alchemy", "teth", false)
	require.NoError(t, err)

	assert.Equal(t, Plan{
		Provider: "alchemy", ChainID: "teth", Network: models.NetworkEthereumSepolia, ProviderNetwork: "ETH_SEPOLIA",
		WebhookURL: testBaseURL + "/v1/webhooks/ingest/alchemy/teth", Addresses: 2,
	}, plan)
	assert.Empty(t, f.creator.configs)
	assert.Empty(t, f.store.rows)
	assert.Empty(t, f.sync.chains)
}

func TestSubscribe_Apply_CreatesTheSubscriptionStoresItSealedAndSyncsTheAddresses(t *testing.T) {
	f := newFixture(t, nil)

	plan, err := subscribe(f, " Alchemy ", "teth", true)
	require.NoError(t, err)

	require.Len(t, f.creator.configs, 1)
	assert.Equal(t, providers.ProviderConfig{
		ChainID: "teth", Network: "ETH_SEPOLIA", WebhookURL: testBaseURL + "/v1/webhooks/ingest/alchemy/teth",
		Addresses: []string{"0xalice", "0xbob"},
	}, f.creator.configs[0])
	require.Len(t, f.store.rows, 1)
	row := f.store.rows[0]
	assert.NotEqual(t, uuid.Nil, row.ID)
	assert.Equal(t, "teth", row.ChainID)
	assert.Equal(t, "alchemy", row.Provider)
	assert.Equal(t, "wh_123", row.ProviderWebhookID)
	assert.Equal(t, testBaseURL+"/v1/webhooks/ingest/alchemy/teth", row.WebhookURL)
	assert.Equal(t, "enc:v1:sealed("+testSecret+")", row.SigningSecret, "the signing secret is stored sealed")
	assert.Equal(t, models.WebhookSubscriptionActive, row.Status)
	assert.Equal(t, []string{"teth"}, f.sync.chains, "the address sync runs once the row exists")
	assert.True(t, plan.Applied)
	assert.Equal(t, row.ID, plan.SubscriptionID)
	assert.Equal(t, "wh_123", plan.ProviderWebhookID)
	requireNoCredential(t, fmt.Sprintf("%+v", plan))
}

func TestSubscribe_ExplainsEveryMissingSettingAndCallsNothing(t *testing.T) {
	for _, apply := range []bool{false, true} {
		f := newFixture(t, func(d *Deps) { d.BaseURL = "" })
		f.keys = map[string]string{}

		plan, err := subscribe(f, "alchemy", "teth", apply)

		require.ErrorIs(t, err, ErrMissingConfiguration)
		require.Len(t, plan.Missing, 2)
		assert.Contains(t, plan.Missing[0], "WEBHOOK_INGEST_BASE_URL")
		assert.Contains(t, plan.Missing[0], "vault.webhooks.ingest_base_url")
		assert.Contains(t, plan.Missing[1], "ALCHEMY_AUTH_TOKEN")
		assert.Contains(t, plan.Missing[1], "vault.webhooks.alchemy_auth_token")
		assert.Contains(t, plan.Missing[1], "provider_alchemy")
		assert.Empty(t, f.creator.configs)
		assert.Empty(t, f.store.rows)
	}
}

func TestSubscribe_NamesTheKeyOfEachProvider(t *testing.T) {
	f := newFixture(t, nil)
	f.keys = map[string]string{}

	plan, err := subscribe(f, "helius", "tsol", false)

	require.ErrorIs(t, err, ErrMissingConfiguration)
	require.Len(t, plan.Missing, 1)
	assert.Contains(t, plan.Missing[0], "HELIUS_API_KEY")
}

func TestSubscribe_RefusesABaseURLAProviderCannotPostTo(t *testing.T) {
	for _, base := range []string{"http://wallets.example.com", "wallets.example.com", "https://", "https://user:pass@wallets.example.com", "https://wallets.example.com/?a=1"} {
		f := newFixture(t, func(d *Deps) { d.BaseURL = base })

		_, err := subscribe(f, "alchemy", "teth", false)
		assert.ErrorIs(t, err, ErrInvalidBaseURL, base)
	}
}

func TestSubscribe_KeepsTheBaseURLPath(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.BaseURL = "https://wallets.example.com/waas/" })

	plan, err := subscribe(f, "alchemy", "teth", false)
	require.NoError(t, err)
	assert.Equal(t, "https://wallets.example.com/waas/v1/webhooks/ingest/alchemy/teth", plan.WebhookURL)
}

func TestSubscribe_RefusesAnUnknownProviderAChainItDoesNotServeAndAnUnknownChain(t *testing.T) {
	f := newFixture(t, nil)

	_, err := subscribe(f, "etherscan", "teth", false)
	assert.ErrorIs(t, err, ErrUnknownProvider)

	_, err = subscribe(f, "alchemy", "ttron", false)
	assert.ErrorIs(t, err, ErrUnsupportedChain)

	_, err = subscribe(f, "alchemy", "nochain", false)
	assert.ErrorIs(t, err, models.ErrRepositoryNotFound)
}

func TestSubscribe_ASubscriptionThatExistsIsLeftAsItIs(t *testing.T) {
	f := newFixture(t, nil)
	existing := models.WebhookSubscription{ID: uuid.New(), ChainID: "teth", Provider: "alchemy", ProviderWebhookID: "wh_old", Status: models.WebhookSubscriptionActive}
	f.store.rows = append(f.store.rows, existing)

	plan, err := subscribe(f, "alchemy", "teth", true)
	require.NoError(t, err)

	require.NotNil(t, plan.Existing)
	assert.Equal(t, existing.ID, plan.Existing.ID)
	assert.False(t, plan.Applied)
	assert.Empty(t, f.creator.configs, "a second provider webhook would push every deposit twice")
	assert.Len(t, f.store.rows, 1)
}

func TestSubscribe_RefusesASecondProviderOnAChain(t *testing.T) {
	f := newFixture(t, nil)
	f.keys[providers.ProviderHelius] = "helius-key"
	f.store.rows = append(f.store.rows, models.WebhookSubscription{ID: uuid.New(), ChainID: "tsol", Provider: "alchemy", Status: models.WebhookSubscriptionActive})

	_, err := subscribe(f, "helius", "tsol", true)
	assert.ErrorIs(t, err, ErrChainSubscribed)
	assert.Empty(t, f.creator.configs)
}

func TestSubscribe_DeletesTheProviderWebhookWhenItCannotBeStored(t *testing.T) {
	cases := map[string]func(*fixture){
		"no signing secret": func(f *fixture) { f.creator.webhook = &providers.ProviderWebhook{ProviderWebhookID: "wh_123"} },
		"store fails":       func(f *fixture) { f.store.createErr = errors.New("connection refused") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			breakIt(f)

			plan, err := subscribe(f, "alchemy", "teth", true)

			require.Error(t, err)
			assert.False(t, plan.Applied)
			assert.Equal(t, []string{"wh_123"}, f.creator.deleted, "no provider webhook is left without its row")
			assert.Empty(t, f.store.rows)
			assert.Empty(t, f.sync.chains)
			requireNoCredential(t, err.Error())
		})
	}
}

func TestSubscribe_DeletesTheProviderWebhookWhenTheSecretCannotBeSealed(t *testing.T) {
	f := newFixture(t, func(d *Deps) {
		d.Seal = func(string) (string, error) { return "", errors.New("cipher unavailable") }
	})

	_, err := subscribe(f, "alchemy", "teth", true)

	require.Error(t, err)
	assert.Equal(t, []string{"wh_123"}, f.creator.deleted)
	assert.Empty(t, f.store.rows)
	requireNoCredential(t, err.Error())
}

func TestSubscribe_NamesAProviderWebhookItCouldNotDelete(t *testing.T) {
	f := newFixture(t, nil)
	f.store.createErr = errors.New("connection refused")
	f.creator.deleteErr = errors.New("provider down")

	_, err := subscribe(f, "alchemy", "teth", true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wh_123", "the operator must be able to delete it by hand")
}

func TestSubscribe_ReportsAFailedSyncAfterTheRowIsStored(t *testing.T) {
	f := newFixture(t, nil)
	f.sync.err = errors.New("provider timeout")

	plan, err := subscribe(f, "alchemy", "teth", true)

	require.ErrorIs(t, err, ErrAddressSyncFailed)
	assert.True(t, plan.Applied)
	assert.Len(t, f.store.rows, 1, "the subscription stays: the reconciler syncs it again")
	assert.Empty(t, f.creator.deleted)
}

func TestSubscribe_ReturnsAProviderFailureWithoutStoringAnything(t *testing.T) {
	f := newFixture(t, nil)
	f.creator.createErr = errors.New("alchemy: 401")

	_, err := subscribe(f, "alchemy", "teth", true)

	require.Error(t, err)
	assert.Empty(t, f.store.rows)
	assert.Empty(t, f.creator.deleted)
}

func TestList_Subscriptions_ShowsTheHostAndNeverTheSecret(t *testing.T) {
	f := newFixture(t, nil)
	synced := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	f.store.rows = append(f.store.rows, models.WebhookSubscription{
		ID: uuid.New(), ChainID: "teth", Provider: "alchemy", ProviderWebhookID: "wh_123",
		WebhookURL: "https://wallets.example.com/v1/webhooks/ingest/alchemy/teth", SigningSecret: "enc:v1:" + testSecret,
		Status: models.WebhookSubscriptionActive, SyncStatus: "synced", LastSyncedAt: &synced,
	})

	listed, err := f.svc.List(context.Background())
	require.NoError(t, err)

	require.Len(t, listed, 1)
	assert.Equal(t, Listed{
		ID: f.store.rows[0].ID, Provider: "alchemy", ChainID: "teth", ProviderWebhookID: "wh_123",
		Host: "wallets.example.com", Status: "active", SyncStatus: "synced", LastSyncedAt: &synced,
	}, listed[0])
	assert.False(t, strings.Contains(fmt.Sprintf("%+v", listed), testSecret))
}
