package providers

// alchemyProvider is the live Alchemy client. The ingest adapter registers it.
var alchemyProvider func(apiKey string) WebhookProvider

// SetAlchemyProvider registers the Alchemy HTTP client. The adapter calls it
// from init. Callers keep WebhookProvider.
func SetAlchemyProvider(factory func(apiKey string) WebhookProvider) {
	alchemyProvider = factory
}

// NewAlchemyProvider returns the registered client. An empty apiKey is the
// inbound fallback; the composition root installs a KeySource on its own
// instance. A process that does not link the adapter gets nil.
func NewAlchemyProvider(apiKey string) WebhookProvider {
	if alchemyProvider == nil {
		return nil
	}
	return alchemyProvider(apiKey)
}
