package coingecko

import "github.com/macrowallets/waas/app/services/price"

func init() {
	price.SetCoinGeckoProvider(func(apiKey string) price.PriceProvider {
		return NewCoinGeckoProvider(apiKey)
	})
}
