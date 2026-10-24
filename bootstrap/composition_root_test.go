package bootstrap_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/ingestsubscription"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// The providers key every service by its type (a typed nil pointer); the
// framework keys its own bindings by string. Every typed binding must build,
// hold the type it is keyed by, and come back as the same instance: a
// singleton built twice would split the state of the services that share it.
func TestComposition_Root_ResolvesEveryTypedBindingToOneInstance(t *testing.T) {
	var keys []any
	for _, key := range app.Bindings() {
		if typed := reflect.TypeOf(key); typed != nil && typed.Kind() == reflect.Pointer && reflect.ValueOf(key).IsNil() {
			keys = append(keys, key)
		}
	}
	require.NotEmpty(t, keys, "no typed binding is registered")

	for _, key := range keys {
		t.Run(fmt.Sprintf("%T", key), func(t *testing.T) {
			first, err := app.Make(key)
			require.NoError(t, err)
			require.False(t, container.IsNil(first), "the binding returned nil")
			assert.Equal(t, reflect.TypeOf(key), reflect.TypeOf(first), "the binding returned another type")

			second, err := app.Make(key)
			require.NoError(t, err)
			assert.True(t, first == second, "a second resolution built another instance")
		})
	}
}

// The registry the registry service fills is the one every consumer gets.
func TestComposition_Root_SharesTheChainRegistry(t *testing.T) {
	registry := container.MustMake[*chainpkg.Registry]()
	assert.Same(t, container.MustMake[*chainregistry.ChainRegistryService]().Registry(), registry)
}

// The chain provider keys the RPC endpoint fingerprints with APP_KEY before any
// client is built, so every process names an endpoint the same way and an unkeyed
// digest cannot be matched.
func TestComposition_Root_KeysTheRPCEndpointFingerprints(t *testing.T) {
	const url = "http://5.104.72.66:8545"
	keyed := chainpkg.RPCEndpointFingerprint(url)
	chainpkg.SetRPCFingerprintKey(nil)
	unkeyed := chainpkg.RPCEndpointFingerprint(url)
	chainpkg.SetRPCFingerprintKey([]byte(appfacades.Config().GetString("app.key")))

	assert.NotEqual(t, unkeyed, keyed)
	assert.Equal(t, keyed, chainpkg.RPCEndpointFingerprint(url))
}

// Every webhook URL goes through one guard: the delivery client the providers
// register refuses a cloud metadata address before it connects, and the webhook
// service, which the dashboard wallet webhooks ask too, refuses it on registration.
func TestComposition_Root_GuardsEveryWebhookDestination(t *testing.T) {
	const metadata = "http://169.254.169.254/latest/meta-data/iam/security-credentials/"
	client := webhook.NewDeliveryClient()
	require.NotNil(t, client, "no webhook delivery client is registered")
	_, err := client.Post(context.Background(), webhook.SignedDelivery{
		URL: metadata, Body: []byte("{}"), Signature: "sig", SignatureV2: "t=1760950000,v1=sig", Timestamp: 1760950000,
		EventType: "deposit.confirmed", DeliveryID: "composition-1", Timeout: time.Second,
	})
	assert.ErrorIs(t, err, webhook.ErrURLNotAllowed)
	assert.True(t, httpclient.IsBuild(err), "a refused destination must read as never sent")

	webhooks := container.MustMake[*webhook.Service]()
	assert.ErrorIs(t, webhooks.CheckURL(context.Background(), metadata), webhook.ErrURLNotAllowed)
	assert.ErrorIs(t, webhooks.CheckURL(context.Background(), "http://hooks.example.com/in"), webhook.ErrURLNotAllowed,
		"http is refused unless WEBHOOK_ALLOW_HTTP is set")
	_, err = container.MustMake[*walletrecords.Webhooks]().Register(context.Background(), uuid.New(), metadata, "s", "deposit.confirmed")
	assert.ErrorIs(t, err, webhook.ErrURLNotAllowed)
}

// Every connection of the shared provider transport (RPC nodes, chain and provider
// APIs) opens through the RPC guard the chain provider installs: a link-local or
// metadata destination is refused before anything is sent.
func TestComposition_Root_GuardsEveryProviderConnection(t *testing.T) {
	for _, url := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[fd00:ec2::254]/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://rpc.example.com:25/",
	} {
		_, err := httpclient.NewClient(time.Second).Do(context.Background(), httpclient.Request{Method: httpclient.MethodGet, URL: url})
		assert.ErrorIs(t, err, egress.ErrRefused, url)
	}
}

// The ingest base URL has one guard, bound under its own type: egress.PublicBaseGuard,
// the only type the subscription and the settings services take for it, so no other
// guard (the webhook receivers', which takes loopback under APP_ENV=testing, or the
// RPC nodes') compiles in its place. It is the zero policy whatever the webhook
// settings say: loopback, private and link-local destinations and http are refused,
// and a public host on another port than 443 is taken. Literals only, so nothing
// here asks the system resolver.
func TestComposition_Root_GuardsTheIngestBaseURLWithTheZeroPolicy(t *testing.T) {
	guard := container.MustMake[*egress.PublicBaseGuard]()
	ctx := context.Background()

	for raw, reason := range map[string]string{
		"https://127.0.0.1":           egress.ReasonLoopback,
		"https://10.0.0.5":            egress.ReasonPrivate,
		"https://169.254.169.254":     egress.ReasonLinkLocal,
		"http://93.184.215.14":        egress.ReasonScheme,
		"https://93.184.215.14:25":    egress.ReasonPort,
		"https://93.184.215.14/?ok=1": egress.ReasonBaseURL,
	} {
		err := guard.CheckPublicBase(ctx, raw)
		var refusal *egress.Refusal
		require.ErrorAs(t, err, &refusal, raw)
		assert.Equal(t, reason, refusal.Reason, raw)
	}
	assert.NoError(t, guard.CheckPublicBase(ctx, "https://93.184.215.14:8443/waas"))
	assert.Same(t, guard, container.MustMake[*egress.PublicBaseGuard](), "one guard for both services")
	assert.NotNil(t, container.MustMake[*ingestsubscription.Service]())
	assert.NotNil(t, container.MustMake[*settings.Service]())
}

// The bound subscription service reads the ingest base URL and the provider key on
// every run, the platform settings before the environment: a value saved through the
// settings service after the service was built applies to the next run, and
// WEBHOOK_INGEST_BASE_URL is read again once the setting is cleared.
func TestComposition_Root_ReadsTheIngestBaseURLAndProviderKeyFromThePlatformSettingsAtEachRun(t *testing.T) {
	fixtures.TestDB(t)
	setConfig(t, "vault.webhooks.ingest_base_url", "https://env.wallets.test")
	setConfig(t, "vault.webhooks.helius_api_key", "")
	ctx := context.Background()
	require.NoError(t, container.MustMake[*repositories.ChainRepository]().Create(ctx, &models.Chain{
		ID: "tsol", Name: "tsol", AdapterType: models.AdapterTypeSolana, NativeSymbol: "SOL", NativeDecimals: 9,
		RpcURL: "enc", RequiredConfirmations: 1, IsTestnet: true, Status: "active", DisplayOrder: 1,
	}))
	admin := fixtures.InsertUser(t)
	_, err := appfacades.Orm().Query().Exec(`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`, admin.ID)
	require.NoError(t, err)
	subscriptions := container.MustMake[*ingestsubscription.Service]()
	platform := container.MustMake[*settings.Service]()
	dryRun := ingestsubscription.Request{Provider: "helius", ChainID: "tsol"}
	const fromEnv = "WEBHOOK_INGEST_BASE_URL (vault.webhooks.ingest_base_url)"

	plan, err := subscriptions.Subscribe(ctx, dryRun)
	require.ErrorIs(t, err, ingestsubscription.ErrMissingConfiguration, "no Helius key is configured yet")
	assert.Equal(t, "https://env.wallets.test/v1/webhooks/ingest/helius/tsol", plan.WebhookURL)
	assert.Equal(t, fromEnv, plan.BaseURLSource)
	require.Len(t, plan.Missing, 1)
	assert.Contains(t, plan.Missing[0], "HELIUS_API_KEY")

	_, err = platform.SavePlatform(ctx, admin.ID, "ingest_push", map[string]any{"base_url": "https://push.wallets.test/waas"})
	require.NoError(t, err)
	_, err = platform.SavePlatform(ctx, admin.ID, "provider_helius", map[string]any{"enabled": true, "api_key": "helius-composition-key"})
	require.NoError(t, err)

	plan, err = subscriptions.Subscribe(ctx, dryRun)
	require.NoError(t, err)
	assert.Equal(t, "https://push.wallets.test/waas/v1/webhooks/ingest/helius/tsol", plan.WebhookURL)
	assert.Equal(t, "the ingest_push.base_url platform setting", plan.BaseURLSource)
	assert.Empty(t, plan.Missing, "the provider_helius key stands in for HELIUS_API_KEY")
	assert.False(t, plan.Applied, "a dry run calls no provider")

	_, err = platform.SavePlatform(ctx, admin.ID, "ingest_push", map[string]any{"base_url": ""})
	require.NoError(t, err)
	plan, err = subscriptions.Subscribe(ctx, dryRun)
	require.NoError(t, err)
	assert.Equal(t, "https://env.wallets.test/v1/webhooks/ingest/helius/tsol", plan.WebhookURL)
	assert.Equal(t, fromEnv, plan.BaseURLSource)
}

// setConfig sets one configuration value for the test and puts the previous one back.
func setConfig(t *testing.T, key, value string) {
	t.Helper()
	cfg := appfacades.Config()
	previous := cfg.GetString(key)
	cfg.Add(key, value)
	t.Cleanup(func() { cfg.Add(key, previous) })
}
