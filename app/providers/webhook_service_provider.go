package providers

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/goravel/framework/contracts/foundation"

	alchemyingest "github.com/macrowallets/waas/app/adapters/ingest/alchemy"
	heliusingest "github.com/macrowallets/waas/app/adapters/ingest/helius"
	quicknodeingest "github.com/macrowallets/waas/app/adapters/ingest/quicknode"
	queuesqs "github.com/macrowallets/waas/app/adapters/queue/sqs"

	// Link the webhook delivery HTTP client. The service still signs each post.
	_ "github.com/macrowallets/waas/app/adapters/webhook/delivery"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/ingestsubscription"
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
	app.Singleton((*ingestsubscription.Service)(nil), func(app foundation.Application) (any, error) {
		return newIngestSubscriptionService(app)
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
		SQS: queuesqs.New(
			queuesqs.NewClient(*awsCfg, facades.Config().GetString("vault.aws.endpoint_url")),
			facades.Config().GetString("vault.queues.webhook"),
		),
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
		OpenSecret:    openSigningSecret,
	}), nil
}

// newIngestSubscriptionService creates provider subscriptions with the catalog's
// provider instances, which read their key at the call, and syncs their addresses
// through the subscription sync.
func newIngestSubscriptionService(app foundation.Application) (*ingestsubscription.Service, error) {
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
	sync, err := resolve[*webhooksync.Service](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, err
	}
	chainRows, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return nil, err
	}
	creators := make(map[string]ingestsubscription.Creator, len(catalog.Lookup()))
	for name, provider := range catalog.Lookup() {
		creators[name] = provider
	}
	return ingestsubscription.NewService(ingestsubscription.Deps{
		Subscriptions: subscriptions,
		Addresses:     addresses,
		Sync:          sync,
		Creators:      creators,
		ProviderKey:   ingestProviderKey(accountSettings),
		ChainNetwork:  chainNetwork(chainRows),
		BaseURL:       facades.Config().GetString("vault.webhooks.ingest_base_url"),
		Seal: func(plaintext string) (string, error) {
			return settings.Seal(facades.LateCrypt(), plaintext)
		},
	}), nil
}

// chainNetwork names the network a chain record serves, read with its RPC URL so a
// Bitcoin testnet record pointed at testnet4 is named so; a URL that does not open
// leaves the record's own reading.
func chainNetwork(chains *repositories.ChainRepository) func(ctx context.Context, chainID string) (string, error) {
	return func(ctx context.Context, chainID string) (string, error) {
		row, err := chains.FindByID(ctx, chainID)
		if err != nil {
			return "", err
		}
		rpcURL, opened := openedRPCURL(row.RpcURL)
		if !opened {
			return row.Network(), nil
		}
		return row.ResolveNetwork(rpcURL).Name, nil
	}
}

// openedRPCURL is a chain's stored RPC URL opened and resolved; false when it does not
// open (the URL is never logged).
func openedRPCURL(stored string) (string, bool) {
	opened, err := settings.OpenStored(facades.LateCrypt(), stored)
	if err != nil {
		return "", false
	}
	rpcURL, err := models.ResolveRPCURL(opened)
	return rpcURL, err == nil
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

// openSigningSecret opens a stored subscription signing secret: sealed (enc:v1:), or
// the bare Crypt envelope of a row written before the seal.
func openSigningSecret(stored string) (string, error) {
	return settings.OpenStored(facades.LateCrypt(), stored)
}
