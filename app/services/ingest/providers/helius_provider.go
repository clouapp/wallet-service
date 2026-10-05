package providers

// heliusProvider is the live Helius client. The ingest adapter registers it.
var heliusProvider func(apiKey string) WebhookProvider

// SetHeliusProvider registers the Helius HTTP client. The adapter calls it
// from init. Callers keep WebhookProvider.
func SetHeliusProvider(factory func(apiKey string) WebhookProvider) {
	heliusProvider = factory
}

// NewHeliusProvider returns the registered client. An empty apiKey is the
// inbound fallback; the composition root installs a KeySource on its own
// instance. A process that does not link the adapter gets nil.
func NewHeliusProvider(apiKey string) WebhookProvider {
	if heliusProvider == nil {
		return nil
	}
	return heliusProvider(apiKey)
}
