package price

// coinAPIProvider is the live CoinAPI REST client. The price adapter registers it.
var coinAPIProvider func(apiKey string) PriceProvider

// SetCoinAPIProvider registers the CoinAPI REST client. The adapter calls it
// from init. Callers keep PriceProvider.
func SetCoinAPIProvider(factory func(apiKey string) PriceProvider) {
	coinAPIProvider = factory
}

// NewCoinAPIProvider returns the registered REST client. A process that does not
// link the adapter gets nil. The websocket dialer is separate.
func NewCoinAPIProvider(apiKey string) PriceProvider {
	if coinAPIProvider == nil {
		return nil
	}
	return coinAPIProvider(apiKey)
}
