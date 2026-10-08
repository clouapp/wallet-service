package solana

import (
	"math/big"

	"github.com/macrowallets/waas/pkg/amount"
)

func fmtUnits(units *big.Int, decimals uint8) string {
	if units == nil {
		return "0"
	}
	return amount.FormatBaseUnits(units, int(decimals))
}
