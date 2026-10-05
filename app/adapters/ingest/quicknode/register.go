package quicknode

import "github.com/macrowallets/waas/app/services/ingest/providers"

func init() {
	providers.SetQuickNodeProvider(func(apiKey string) providers.WebhookProvider {
		return NewQuickNodeProvider(apiKey)
	})
}
