package price

import (
	"context"
	"log/slog"
	"strings"
)

const (
	providerCoinGecko     = "coingecko"
	providerCoinMarketCap = "coinmarketcap"
	providerCoinAPI       = "coinapi"
	sealedKeyPrefix       = "enc:v1:"
)

// Credential is one provider selected for this quote. Key is the opened
// plaintext. Callers must not log, print, or return Key or its ciphertext.
type Credential struct {
	Name string
	Key  string
}

// SettingsSource resolves key, enabled, and provider order for one quote.
// Implementations open sealed keys on each call. An error or an empty list
// means no settings provider is usable.
type SettingsSource func(ctx context.Context) ([]Credential, error)

// quoteProviderFactory builds one provider from a name and an opened key.
// ok is false for an unknown name. The key must not be logged.
type quoteProviderFactory func(name, apiKey string) (PriceProvider, bool)

// WithSettingsSource installs the per-quote reader. Nil keeps the providers
// passed to NewService.
func (s *Service) WithSettingsSource(source SettingsSource) *Service {
	if s == nil {
		return nil
	}
	s.settings = source
	return s
}

// WithEnvCoinAPIKey keeps today's environment CoinAPI key for a quote that
// has no usable settings provider. The client is built when that quote runs.
func (s *Service) WithEnvCoinAPIKey(key string) *Service {
	if s == nil {
		return nil
	}
	s.envCoinAPI = strings.TrimSpace(key)
	return s
}

func (s *Service) withProviderFactory(factory quoteProviderFactory) *Service {
	if s == nil {
		return nil
	}
	s.newProvider = factory
	return s
}

// providersForQuote asks SettingsSource once. A missing source keeps the
// providers given to NewService. A failed read, an empty list, or a list
// with no usable provider falls back to the environment CoinAPI key and
// does not fail the quote.
func (s *Service) providersForQuote(ctx context.Context) []PriceProvider {
	if s == nil || s.settings == nil {
		if s == nil {
			return nil
		}
		return s.providers
	}
	credentials, err := s.settings(ctx)
	if err != nil {
		slog.Warn("price settings were not read")
		return s.envCoinAPIProviders()
	}
	built := s.providersFromCredentials(credentials)
	if len(built) == 0 {
		return s.envCoinAPIProviders()
	}
	return built
}

func (s *Service) providersFromCredentials(credentials []Credential) []PriceProvider {
	out := make([]PriceProvider, 0, len(credentials))
	for _, credential := range credentials {
		provider, ok := s.quoteProvider(credential.Name, credential.Key)
		if !ok {
			continue
		}
		out = append(out, provider)
	}
	return out
}

func (s *Service) envCoinAPIProviders() []PriceProvider {
	provider, ok := s.quoteProvider(providerCoinAPI, s.envCoinAPI)
	if !ok {
		return nil
	}
	return []PriceProvider{provider}
}

func (s *Service) quoteProvider(name, apiKey string) (PriceProvider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	apiKey = strings.TrimSpace(apiKey)
	if name == "" || apiKey == "" || strings.HasPrefix(apiKey, sealedKeyPrefix) {
		return nil, false
	}
	if s.newProvider != nil {
		provider, ok := s.newProvider(name, apiKey)
		if !ok || provider == nil {
			return nil, false
		}
		return provider, true
	}
	switch name {
	case providerCoinGecko:
		return NewCoinGeckoProvider(apiKey), true
	case providerCoinMarketCap:
		return NewCoinMarketCapProvider(apiKey), true
	case providerCoinAPI:
		return NewCoinAPIProvider(apiKey), true
	default:
		return nil, false
	}
}
