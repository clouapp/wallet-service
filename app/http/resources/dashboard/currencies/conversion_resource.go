package currencies

import (
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Conversion is the answer to a currency conversion. The fields are in the
// order the JSON object has always been written (sorted by key).
type Conversion struct {
	Amount numeric.Decimal `json:"amount"`
	From   string          `json:"from"`
	Rate   numeric.Decimal `json:"rate"`
	Result numeric.Decimal `json:"result"`
	To     string          `json:"to"`
}

// NewConversion projects a quote.
func NewConversion(quote price.Quote) Conversion {
	return Conversion{
		Amount: numeric.NewDecimal(quote.Amount),
		From:   quote.From,
		Rate:   numeric.NewDecimal(quote.Rate),
		Result: numeric.NewDecimal(quote.Result),
		To:     quote.To,
	}
}
