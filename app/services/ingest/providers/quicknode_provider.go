package providers

// quicknodeProvider is the live QuickNode client. The ingest adapter registers it.
var quicknodeProvider func(apiKey string) WebhookProvider

// SetQuickNodeProvider registers the QuickNode HTTP client. The adapter calls it
// from init. Callers keep WebhookProvider.
func SetQuickNodeProvider(factory func(apiKey string) WebhookProvider) {
	quicknodeProvider = factory
}

// NewQuickNodeProvider returns the registered client. An empty apiKey is the
// inbound fallback; the composition root installs a KeySource on its own
// instance. A process that does not link the adapter gets nil.
func NewQuickNodeProvider(apiKey string) WebhookProvider {
	if quicknodeProvider == nil {
		return nil
	}
	return quicknodeProvider(apiKey)
}
