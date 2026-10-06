package price_test

import (
	"testing"

	"github.com/macrowallets/waas/app/services/price"
)

func TestNewCoinAPIProviderIsTheRegisteredAdapter(t *testing.T) {
	stub := namedProvider{name: "coinapi"}
	price.SetCoinAPIProvider(func(string) price.PriceProvider { return stub })
	provider := price.NewCoinAPIProvider("registered-only")
	if provider == nil || provider.Name() != "coinapi" {
		t.Fatal("CoinAPI provider was not registered")
	}
}
