package providers

import (
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/settings"
	"testing"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	goravelfoundation "github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/stretchr/testify/assert"
)

// stubCipher satisfies the crypt binding this provider resolves while sealing
// webhook secrets. The graph test does not encrypt anything.
type stubCipher struct{}

func (stubCipher) EncryptString(value string) (string, error) { return value, nil }

func (stubCipher) DecryptString(payload string) (string, error) { return payload, nil }

func TestWebhook_Provider_RegistersTheWebhookGraph(t *testing.T) {
	require.NotNil(t, goravelfoundation.App)
	goravelfoundation.App.Singleton(binding.Crypt, func(foundation.Application) (any, error) {
		return stubCipher{}, nil
	})
	(&WebhookServiceProvider{}).Register(goravelfoundation.App)

	configs, err := container.Make[*repositories.WebhookConfigRepository]()
	require.NoError(t, err)
	require.NotNil(t, configs)

	events, err := container.Make[*repositories.WebhookEventRepository]()
	require.NoError(t, err)
	require.NotNil(t, events)

	subscriptions, err := container.Make[*repositories.WebhookSubscriptionRepository]()
	require.NoError(t, err)
	assert.NotNil(t, subscriptions)

	assert.Same(t, configs, container.MustMake[*repositories.WebhookConfigRepository]())
	assert.Same(t, events, container.MustMake[*repositories.WebhookEventRepository]())
}

func TestWebhook_Provider_SealsAndOpensSigningSecretsWithTheApplicationKey(t *testing.T) {
	require.NotNil(t, goravelfoundation.App)
	goravelfoundation.App.Singleton(binding.Crypt, func(foundation.Application) (any, error) {
		return stubCipher{}, nil
	})
	(&WebhookServiceProvider{}).Register(goravelfoundation.App)

	secrets, err := container.Make[*ingest.SigningSecrets]()
	require.NoError(t, err)
	stored, err := secrets.Seal("whsec-1")
	require.NoError(t, err)
	assert.True(t, settings.IsSealed(stored), "stored sealed, never as given")
	opened, err := secrets.Open(stored)
	require.NoError(t, err)
	assert.Equal(t, "whsec-1", opened)
	_, err = secrets.Open("whsec-1")
	assert.Error(t, err, "a plaintext row is refused")
	assert.Same(t, secrets, container.MustMake[*ingest.SigningSecrets]())
}
