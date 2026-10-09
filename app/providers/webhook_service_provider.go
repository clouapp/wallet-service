package providers

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/goravel/framework/contracts/foundation"

	alchemyingest "github.com/macrowallets/waas/app/adapters/ingest/alchemy"
	heliusingest "github.com/macrowallets/waas/app/adapters/ingest/helius"
	quicknodeingest "github.com/macrowallets/waas/app/adapters/ingest/quicknode"
	queuesqs "github.com/macrowallets/waas/app/adapters/queue/sqs"

	// Link the webhook delivery HTTP client. The service still signs each post.
	_ "github.com/macrowallets/waas/app/adapters/webhook/delivery"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
)

// WebhookServiceProvider binds the webhook config, event, and subscription
// repositories by type, the outbound webhook service with its SQS queue, and
// the inbound provider catalog with the subscription sync that uses it.
type WebhookServiceProvider struct{}

func (p *WebhookServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.WebhookConfigRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookConfigRepository(nil, facades.LateCrypt()), nil
	})
	app.Singleton((*repositories.WebhookEventRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookEventRepository(nil), nil
	})
	app.Singleton((*repositories.WebhookSubscriptionRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookSubscriptionRepository(nil), nil
	})
	app.Singleton((*walletrecords.Webhooks)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WebhookConfigRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewWebhooks(store), nil
	})
	app.Singleton((*ingest.Subscriptions)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WebhookSubscriptionRepository](app)
		if err != nil {
			return nil, err
		}
		return ingest.NewSubscriptions(store), nil
	})
	app.Singleton((*webhook.Service)(nil), func(app foundation.Application) (any, error) {
		return newWebhookService(app)
	})
	app.Singleton((*ingest.Catalog)(nil), func(app foundation.Application) (any, error) {
		accountSettings, err := resolve[*settings.Service](app)
		if err != nil {
			return nil, err
		}
		return ingest.NewCatalog(ingestProviders(ingestProviderKey(accountSettings))), nil
	})
	app.Singleton((*webhooksync.Service)(nil), func(app foundation.Application) (any, error) {
		return newWebhookSyncService(app)
	})
}

func (p *WebhookServiceProvider) Boot(foundation.Application) {}

// newWebhookService sends outbound webhooks through the vault.queues.webhook
// queue and reads the delivery attempts and timeout from the settings store
// on each delivery.
func newWebhookService(app foundation.Application) (*webhook.Service, error) {
	awsCfg, err := resolve[*aws.Config](app)
	if err != nil {
		return nil, err
	}
	configs, err := resolve[*repositories.WebhookConfigRepository](app)
	if err != nil {
		return nil, err
	}
	events, err := resolve[*repositories.WebhookEventRepository](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, err
	}
	service := webhook.NewService(webhook.Deps{
		SQS:     queuesqs.New(sqs.NewFromConfig(*awsCfg), facades.Config().GetString("vault.queues.webhook")),
		Configs: configs,
		Events:  events,
	})
	service.SetDeliverySettingsSource(func(ctx context.Context) (webhook.DeliverySettings, error) {
		stored, readErr := accountSettings.EffectiveWebhookDelivery(ctx)
		if readErr != nil {
			return webhook.DeliverySettings{}, readErr
		}
		return webhook.DeliverySettingsFromStored(stored.MaxAttempts, stored.TimeoutSeconds), nil
	})
	return service, nil
}

// newWebhookSyncService keeps the provider subscriptions in step with the
// addresses, through the same provider instances the catalog serves.
func newWebhookSyncService(app foundation.Application) (*webhooksync.Service, error) {
	catalog, err := resolve[*ingest.Catalog](app)
	if err != nil {
		return nil, err
	}
	subscriptions, err := resolve[*repositories.WebhookSubscriptionRepository](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, err
	}
	byName := catalog.Lookup()
	syncers := make(map[string]webhooksync.AddressSyncer, len(byName))
	for name, provider := range byName {
		syncers[name] = provider
	}
	return webhooksync.NewService(webhooksync.Deps{
		Subscriptions: subscriptions,
		Addresses:     addresses,
		Providers:     syncers,
		ProviderKey:   ingestProviderKey(accountSettings),
	}), nil
}

// ingestProviders keeps provider credentials out of the boot snapshot. Each
// provider reads its key when it calls the vendor or verifies a webhook, and
// webhooksync asks again on every sync.
func ingestProviders(keyFor func(ctx context.Context, provider string) string) map[string]providers.WebhookProvider {
	return map[string]providers.WebhookProvider{
		"alchemy":   alchemyingest.NewAlchemyProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "alchemy") }),
		"helius":    heliusingest.NewHeliusProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "helius") }),
		"quicknode": quicknodeingest.NewQuickNodeProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "quicknode") }),
	}
}

// ingestProviderKey reads a provider key from the settings store. A missing
// or unusable settings row falls back to vault.webhooks.*; an empty result
// fails closed.
func ingestProviderKey(accountSettings *settings.Service) func(ctx context.Context, provider string) string {
	return func(ctx context.Context, provider string) string {
		return accountSettings.IngestProviderKey(ctx, provider, facades.Config().GetString(ingestEnvConfigKey(provider)))
	}
}

func ingestEnvConfigKey(provider string) string {
	switch provider {
	case "alchemy":
		return "vault.webhooks.alchemy_auth_token"
	case "helius":
		return "vault.webhooks.helius_api_key"
	case "quicknode":
		return "vault.webhooks.quicknode_api_key"
	default:
		return ""
	}
}
