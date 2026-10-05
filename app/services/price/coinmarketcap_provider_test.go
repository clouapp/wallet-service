package price_test

import (
	"testing"

	_ "github.com/macrowallets/waas/app/adapters/price/coinmarketcap"
	"github.com/macrowallets/waas/app/services/price"
)

func TestNewCoinMarketCapProviderIsTheRegisteredAdapter(t *testing.T) {
	provider := price.NewCoinMarketCapProvider("registered-only")
	if provider == nil || provider.Name() != "coinmarketcap" {
		t.Fatal("CoinMarketCap provider was not registered")
	}
}
