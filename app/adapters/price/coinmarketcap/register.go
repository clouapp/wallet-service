package coinmarketcap

import "github.com/macrowallets/waas/app/services/price"

func init() {
	price.SetCoinMarketCapProvider(func(apiKey string) price.PriceProvider {
		return NewCoinMarketCapProvider(apiKey)
	})
}
