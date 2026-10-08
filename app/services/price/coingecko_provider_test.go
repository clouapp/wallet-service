package price_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/price"
)

type namedProvider struct{ name string }

func (p namedProvider) Name() string { return p.name }

func (namedProvider) FetchCryptoPrices(context.Context, []string) (map[string]decimal.Decimal, error) {
	return nil, nil
}

func (namedProvider) FetchFiatRates(context.Context, []string) (map[string]decimal.Decimal, error) {
	return nil, nil
}

func TestNew_Coin_GeckoProviderIsTheRegisteredAdapter(t *testing.T) {
	stub := namedProvider{name: "coingecko"}
	price.SetCoinGeckoProvider(func(string) price.PriceProvider { return stub })
	provider := price.NewCoinGeckoProvider("registered-only")
	if provider == nil || provider.Name() != "coingecko" {
		t.Fatal("CoinGecko provider was not registered")
	}
}
