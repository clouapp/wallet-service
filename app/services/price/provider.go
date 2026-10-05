package price

import (
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
)

type PriceProvider interface {
	Name() string
	FetchCryptoPrices(codes []string) (map[string]decimal.Decimal, error)
	FetchFiatRates(codes []string) (map[string]decimal.Decimal, error)
}

// InvertRate turns a quote in units per USD into the USD price of one unit, at the
// price column scale. A quote that is not positive yields zero, which callers skip.
func InvertRate(unitsPerUSD decimal.Decimal) decimal.Decimal {
	if !unitsPerUSD.IsPositive() {
		return decimal.Zero
	}
	return decimal.NewFromInt(1).DivRound(unitsPerUSD, models.CurrencyPriceColumn.Scale)
}

func invertRate(unitsPerUSD decimal.Decimal) decimal.Decimal {
	return InvertRate(unitsPerUSD)
}
