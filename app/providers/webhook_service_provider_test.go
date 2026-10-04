package providers

import (
	"testing"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	goravelfoundation "github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

// stubCipher satisfies the crypt binding this provider resolves while sealing
// webhook secrets. The graph test does not encrypt anything.
type stubCipher struct{}

func (stubCipher) EncryptString(value string) (string, error) { return value, nil }

func (stubCipher) DecryptString(payload string) (string, error) { return payload, nil }

func TestWebhookProvider_RegistersTheWebhookGraph(t *testing.T) {
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
	require.NotNil(t, subscriptions)

	require.Same(t, configs, container.MustMake[*repositories.WebhookConfigRepository]())
	require.Same(t, events, container.MustMake[*repositories.WebhookEventRepository]())
}
