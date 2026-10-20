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
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/httpclient"
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
