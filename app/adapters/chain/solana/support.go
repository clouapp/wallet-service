package solana

import (
	"fmt"
	"math/big"
	"strings"
)

func fmtUnits(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Div(amount, d)
	frac := new(big.Int).Mod(amount, d)
	if frac.Sign() == 0 {
		return whole.String()
	}
	fracStr := strings.TrimRight(fmt.Sprintf("%0*s", decimals, frac.String()), "0")
	return whole.String() + "." + fracStr
}
