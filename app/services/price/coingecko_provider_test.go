package price_test

import (
	"testing"

	_ "github.com/macrowallets/waas/app/adapters/price/coingecko"
	"github.com/macrowallets/waas/app/services/price"
)

func TestNewCoinGeckoProviderIsTheRegisteredAdapter(t *testing.T) {
	provider := price.NewCoinGeckoProvider("registered-only")
	if provider == nil || provider.Name() != "coingecko" {
		t.Fatal("CoinGecko provider was not registered")
	}
}
