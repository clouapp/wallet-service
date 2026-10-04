package ingest

import "github.com/macrowallets/waas/app/services/ingest/providers"

// Catalog is the inbound provider set. Each provider carries a KeySource,
// so a lookup does not freeze the credential that was current at boot.
type Catalog struct {
	byName map[string]providers.WebhookProvider
}

// NewCatalog copies the provider pointers. The copy shares the KeySource
// on each provider; it does not copy a credential.
func NewCatalog(byName map[string]providers.WebhookProvider) *Catalog {
	copied := make(map[string]providers.WebhookProvider, len(byName))
	for name, provider := range byName {
		if provider == nil || name == "" {
			continue
		}
		copied[name] = provider
	}
	return &Catalog{byName: copied}
}

// Lookup returns the providers for one verify call.
func (c *Catalog) Lookup() map[string]providers.WebhookProvider {
	if c == nil {
		return nil
	}
	return c.byName
}
