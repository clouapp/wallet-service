package price

// coinGeckoProvider is the live CoinGecko client. The price adapter registers it.
var coinGeckoProvider func(apiKey string) PriceProvider

// SetCoinGeckoProvider registers the CoinGecko HTTP client. The adapter calls it
// from init. Callers keep PriceProvider.
func SetCoinGeckoProvider(factory func(apiKey string) PriceProvider) {
	coinGeckoProvider = factory
}

// NewCoinGeckoProvider returns the registered client. A process that does not
// link the adapter gets nil.
func NewCoinGeckoProvider(apiKey string) PriceProvider {
	if coinGeckoProvider == nil {
		return nil
	}
	return coinGeckoProvider(apiKey)
}
