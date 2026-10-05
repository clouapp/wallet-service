package helius

import "github.com/macrowallets/waas/app/services/ingest/providers"

func init() {
	providers.SetHeliusProvider(func(apiKey string) providers.WebhookProvider {
		return NewHeliusProvider(apiKey)
	})
}
