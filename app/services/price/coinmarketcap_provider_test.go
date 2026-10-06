package price_test

import (
	"testing"

	"github.com/macrowallets/waas/app/services/price"
)

func TestNewCoinMarketCapProviderIsTheRegisteredAdapter(t *testing.T) {
	stub := namedProvider{name: "coinmarketcap"}
	price.SetCoinMarketCapProvider(func(string) price.PriceProvider { return stub })
	provider := price.NewCoinMarketCapProvider("registered-only")
	if provider == nil || provider.Name() != "coinmarketcap" {
		t.Fatal("CoinMarketCap provider was not registered")
	}
}
