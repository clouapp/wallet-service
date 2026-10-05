package price_test

import (
	"testing"

	_ "github.com/macrowallets/waas/app/adapters/price/coinapi"
	"github.com/macrowallets/waas/app/services/price"
)

func TestNewCoinAPIProviderIsTheRegisteredAdapter(t *testing.T) {
	provider := price.NewCoinAPIProvider("registered-only")
	if provider == nil || provider.Name() != "coinapi" {
		t.Fatal("CoinAPI provider was not registered")
	}
}
