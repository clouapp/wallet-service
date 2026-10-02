package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

func TestWebhookProvider_RegistersTheWebhookGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&WebhookServiceProvider{}).Register(foundation.App)

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
