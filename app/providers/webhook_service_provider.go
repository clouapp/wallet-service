package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
)

// WebhookServiceProvider binds the webhook config, event, and subscription
// repositories by type. The SQS client stays in the vault container: it is not
// a database repository, and delivery still uses the same queue URLs.
type WebhookServiceProvider struct{}

func (p *WebhookServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.WebhookConfigRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookConfigRepository(nil), nil
	})
	app.Singleton((*repositories.WebhookEventRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookEventRepository(nil), nil
	})
	app.Singleton((*repositories.WebhookSubscriptionRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWebhookSubscriptionRepository(nil), nil
	})
}

func (p *WebhookServiceProvider) Boot(foundation.Application) {}
