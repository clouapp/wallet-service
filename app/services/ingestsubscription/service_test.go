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
	// onCreate runs before an insert, as another run inserting first or a request
	// whose context ends would.
	onCreate func(*memoryStore)
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
	if m.onCreate != nil {
		m.onCreate(m)
	}
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
	// deleteCtxErr is the state of the context each delete ran with.
	deleteCtxErr []error
}

func (f *fakeCreator) CreateWebhook(_ context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error) {
	f.configs = append(f.configs, cfg)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.webhook, nil
}

func (f *fakeCreator) DeleteWebhook(ctx context.Context, webhookID string) error {
	f.deleted = append(f.deleted, webhookID)
	f.deleteCtxErr = append(f.deleteCtxErr, ctx.Err())
	return f.deleteErr
}

// activatingCreator creates its webhooks paused, as QuickNode streams are.
type activatingCreator struct {
	*fakeCreator
	activated   []string
	activateErr error
	order       *[]string
}

func (a *activatingCreator) ActivateWebhook(_ context.Context, webhookID string) error {
	a.activated = append(a.activated, webhookID)
	if a.order != nil {
		*a.order = append(*a.order, "activate")
	}
	return a.activateErr
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
	// base is what BaseURLAtUse answers; a test changes it between runs as an
	// operator changes the setting, without rebuilding the service.
	base BaseURL
	// baseReads counts the BaseURLAtUse calls.
	baseReads int
}

func newFixture(t *testing.T, mutate func(*Deps)) *fixture {
	t.Helper()
	f := &fixture{
		store:   &memoryStore{},
		creator: &fakeCreator{webhook: &providers.ProviderWebhook{ProviderWebhookID: "wh_123", SigningSecret: testSecret}},
		sync:    &fakeSync{},
		keys:    map[string]string{providers.ProviderAlchemy: testProviderKey},
		base:    BaseURL{URL: testBaseURL, Source: BaseURLFromSetting},
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
		BaseURLAtUse: func(context.Context) BaseURL {
			f.baseReads++
			return f.base
		},
		Seal: func(plaintext string) (string, error) { return "enc:v1:sealed(" + plaintext + ")", nil },
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
		WebhookURL: testBaseURL + "/v1/webhooks/ingest/alchemy/teth", BaseURLSource: "the ingest_push.base_url platform setting",
		Addresses: 2,
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
		f := newFixture(t, nil)
		f.base = BaseURL{Source: BaseURLFromEnv}
		f.keys = map[string]string{}

		plan, err := subscribe(f, "alchemy", "teth", apply)

		require.ErrorIs(t, err, ErrMissingConfiguration)
		require.Len(t, plan.Missing, 2)
		assert.Contains(t, plan.Missing[0], "WEBHOOK_INGEST_BASE_URL")
		assert.Contains(t, plan.Missing[0], "vault.webhooks.ingest_base_url")
		assert.Contains(t, plan.Missing[0], "ingest_push.base_url platform setting")
		assert.Empty(t, plan.BaseURLSource, "no base URL was read")
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
		f := newFixture(t, nil)
		f.base = BaseURL{URL: base, Source: BaseURLFromEnv}

		_, err := subscribe(f, "alchemy", "teth", false)
		assert.ErrorIs(t, err, ErrInvalidBaseURL, base)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read from WEBHOOK_INGEST_BASE_URL", "the error names where the URL was read")
		assert.NotContains(t, err.Error(), base, "the error never carries the URL")
		assert.Empty(t, f.creator.configs)
	}
}

func TestSubscribe_KeepsTheBaseURLPath(t *testing.T) {
	f := newFixture(t, nil)
	f.base.URL = "https://wallets.example.com/waas/"

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

func newQuickNodeFixture(t *testing.T) (*fixture, *activatingCreator) {
	t.Helper()
	activating := &activatingCreator{fakeCreator: &fakeCreator{webhook: &providers.ProviderWebhook{ProviderWebhookID: "stream-1", SigningSecret: testSecret}}}
	f := newFixture(t, func(d *Deps) {
		d.Creators[providers.ProviderQuickNode] = activating
		inner := d.ChainNetwork
		d.ChainNetwork = func(ctx context.Context, chainID string) (string, error) {
			if chainID == "btc" {
				return models.NetworkBitcoinMainnet, nil
			}
			return inner(ctx, chainID)
		}
	})
	f.keys[providers.ProviderQuickNode] = "qn-key"
	return f, activating
}

func TestSubscribe_ActivatesAPausedWebhookOnceItsRowIsStored(t *testing.T) {
	f, activating := newQuickNodeFixture(t)
	var order []string
	activating.order = &order
	f.store.onCreate = func(*memoryStore) { order = append(order, "store") }

	plan, err := subscribe(f, "quicknode", "btc", true)

	require.NoError(t, err)
	assert.True(t, plan.Applied)
	assert.Equal(t, []string{"stream-1"}, activating.activated)
	assert.Equal(t, []string{"store", "activate"}, order, "a stream pushes only once its secret can verify it")
	assert.Equal(t, []string{"btc"}, f.sync.chains)
}

func TestSubscribe_ReportsAFailedActivationAndActivatesOnTheNextRun(t *testing.T) {
	f, activating := newQuickNodeFixture(t)
	activating.activateErr = errors.New("quicknode: 503")

	plan, err := subscribe(f, "quicknode", "btc", true)
	require.ErrorIs(t, err, ErrActivationFailed)
	assert.True(t, plan.Applied, "the row stays: the next run activates it")
	assert.Contains(t, err.Error(), "stream-1")

	activating.activateErr = nil
	plan, err = subscribe(f, "quicknode", "btc", true)
	require.NoError(t, err)
	require.NotNil(t, plan.Existing)
	assert.Equal(t, []string{"stream-1", "stream-1"}, activating.activated)
	assert.Len(t, activating.configs, 1, "no second stream")

	_, err = subscribe(f, "quicknode", "btc", false)
	require.NoError(t, err)
	assert.Len(t, activating.activated, 2, "a dry run activates nothing")
}

func TestSubscribe_TakesALostRaceForTheExistingSubscription(t *testing.T) {
	f := newFixture(t, nil)
	winner := models.WebhookSubscription{ID: uuid.New(), ChainID: "teth", Provider: "alchemy", ProviderWebhookID: "wh_winner", Status: models.WebhookSubscriptionActive}
	f.store.onCreate = func(m *memoryStore) {
		m.rows = append(m.rows, winner)
		m.createErr = errors.New(`ERROR: duplicate key value violates unique constraint "webhook_subscriptions_active_chain_provider_unique" (SQLSTATE 23505)`)
	}

	plan, err := subscribe(f, "alchemy", "teth", true)

	require.NoError(t, err)
	require.NotNil(t, plan.Existing)
	assert.Equal(t, winner.ID, plan.Existing.ID)
	assert.False(t, plan.Applied)
	assert.Equal(t, []string{"wh_123"}, f.creator.deleted, "the losing run's webhook is deleted")
	assert.Empty(t, f.sync.chains)
}

func TestSubscribe_ASubscriptionThatNoLongerPushesIsNotTheExistingOne(t *testing.T) {
	f := newFixture(t, nil)
	f.store.rows = append(f.store.rows, models.WebhookSubscription{ID: uuid.New(), ChainID: "teth", Provider: "alchemy", ProviderWebhookID: "wh_old", Status: "disabled"})

	plan, err := subscribe(f, "alchemy", "teth", true)

	require.NoError(t, err)
	assert.Nil(t, plan.Existing)
	assert.True(t, plan.Applied)
	assert.Len(t, f.creator.configs, 1)
}

func TestSubscribe_DeletesTheWebhookWithAContextThatOutlivesTheRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t, nil)
	f.store.onCreate = func(m *memoryStore) {
		cancel() // the operator interrupted the run while the row was written
		m.createErr = errors.New("context canceled")
	}

	_, err := f.svc.Subscribe(ctx, Request{Provider: "alchemy", ChainID: "teth", Apply: true})

	require.Error(t, err)
	require.Equal(t, []string{"wh_123"}, f.creator.deleted)
	assert.NoError(t, f.creator.deleteCtxErr[0], "the delete is not cancelled with the request")
}

func TestSubscribe_SendsAnEmptyAddressListRatherThanNone(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.Addresses = fixedAddresses(nil) })

	_, err := subscribe(f, "alchemy", "teth", true)

	require.NoError(t, err)
	require.Len(t, f.creator.configs, 1)
	assert.NotNil(t, f.creator.configs[0].Addresses, "Alchemy refuses addresses: null")
	assert.Empty(t, f.creator.configs[0].Addresses)
}

func TestSubscribe_RefusesABaseURLThatIsNotAPublicHost(t *testing.T) {
	for _, base := range []string{
		"https://localhost", "https://api.localhost", "https://127.0.0.1", "https://10.0.0.5", "https://172.16.4.1",
		"https://192.168.1.10", "https://169.254.169.254", "https://[::1]", "https://[fd00::1]", "https://[fe80::1]",
		"https://0.0.0.0", "https://100.64.0.1", "https://wallets.example.com:99999", "https://wallets.example.com:http",
		"https://wallets.example.com#x", "https://wallets.example.com#", "https://wallets.example.com?",
	} {
		f := newFixture(t, nil)
		f.base.URL = base
		_, err := subscribe(f, "alchemy", "teth", false)
		assert.ErrorIs(t, err, ErrInvalidBaseURL, base)
	}

	f := newFixture(t, nil)
	f.base.URL = "https://wallets.example.com:8443"
	plan, err := subscribe(f, "alchemy", "teth", false)
	require.NoError(t, err)
	assert.Equal(t, "https://wallets.example.com:8443/v1/webhooks/ingest/alchemy/teth", plan.WebhookURL)
}

func TestSubscribe_ReadsTheBaseURLAtEachRunSoAChangedSettingAppliesWithoutARebuild(t *testing.T) {
	f := newFixture(t, nil)
	f.base = BaseURL{URL: "https://env.wallets.example.com", Source: BaseURLFromEnv}

	plan, err := subscribe(f, "alchemy", "teth", false)
	require.NoError(t, err)
	assert.Equal(t, "https://env.wallets.example.com/v1/webhooks/ingest/alchemy/teth", plan.WebhookURL)
	assert.Equal(t, "WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)", plan.BaseURLSource)

	// The operator saves the platform setting; the same service runs again.
	f.base = BaseURL{URL: "https://push.wallets.example.com/waas", Source: BaseURLFromSetting}
	plan, err = subscribe(f, "alchemy", "teth", true)
	require.NoError(t, err)
	assert.Equal(t, "https://push.wallets.example.com/waas/v1/webhooks/ingest/alchemy/teth", plan.WebhookURL)
	assert.Equal(t, "the ingest_push.base_url platform setting", plan.BaseURLSource)
	require.Len(t, f.creator.configs, 1)
	assert.Equal(t, plan.WebhookURL, f.creator.configs[0].WebhookURL, "the provider is pointed at the URL of this run")
	require.Len(t, f.store.rows, 1)
	assert.Equal(t, plan.WebhookURL, f.store.rows[0].WebhookURL)
	assert.Equal(t, 2, f.baseReads, "the base URL is read once per run, never kept from construction")
}

func TestSubscribe_WithoutABaseURLSourceNamesTheBaseURLMissing(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.BaseURLAtUse = nil })

	plan, err := subscribe(f, "alchemy", "teth", false)

	require.ErrorIs(t, err, ErrMissingConfiguration)
	require.Len(t, plan.Missing, 1)
	assert.Contains(t, plan.Missing[0], "WEBHOOK_INGEST_BASE_URL")
	assert.Empty(t, plan.WebhookURL)
}
