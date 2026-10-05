package alchemy

import "github.com/macrowallets/waas/app/services/ingest/providers"

func init() {
	providers.SetAlchemyProvider(func(apiKey string) providers.WebhookProvider {
		return NewAlchemyProvider(apiKey)
	})
}
