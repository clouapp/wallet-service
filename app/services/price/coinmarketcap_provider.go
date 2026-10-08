package price

// coinMarketCapProvider is the live CoinMarketCap REST client. The price adapter registers it.
var coinMarketCapProvider func(apiKey string) PriceProvider

// SetCoinMarketCapProvider registers the CoinMarketCap REST client. The adapter calls it
// from init. Callers keep PriceProvider.
func SetCoinMarketCapProvider(factory func(apiKey string) PriceProvider) {
	coinMarketCapProvider = factory
}

// NewCoinMarketCapProvider returns the registered REST client. A process that does not
// link the adapter gets nil.
func NewCoinMarketCapProvider(apiKey string) PriceProvider {
	if coinMarketCapProvider == nil {
		return nil
	}
	return coinMarketCapProvider(apiKey)
}
