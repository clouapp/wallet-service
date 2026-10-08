package models

import "github.com/macrowallets/waas/pkg/numeric"

// Numeric column definitions, matching the migrations; values are validated or fitted
// against them before they are written.
var (
	CurrencyPriceColumn    = numeric.Column{Name: "currency price", Precision: 28, Scale: 10}
	USDValueColumn         = numeric.Column{Name: "usd value", Precision: 28, Scale: 10}
	FeeMultiplierColumn    = numeric.Column{Name: "fee_multiplier", Precision: 8, Scale: 4}
	DustThresholdUSDColumn = numeric.Column{Name: "dust_threshold_usd", Precision: 16, Scale: 4}
)
