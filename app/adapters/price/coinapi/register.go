package coinapi

import "github.com/macrowallets/waas/app/services/price"

func init() {
	price.SetCoinAPIProvider(func(apiKey string) price.PriceProvider {
		return NewCoinAPIProvider(apiKey)
	})
}
