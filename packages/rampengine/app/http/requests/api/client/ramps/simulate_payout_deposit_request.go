package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// SimulatePayoutDepositRequest validates POST
// /api/offramps/address/simulate-deposit: the route of the address, and how
// much of the token the sandbox pretends the user sent.
type SimulatePayoutDepositRequest struct {
	requests.Open
	Rail          string `form:"rail" json:"rail"`
	BankAccountID uint   `form:"bank_account_id" json:"bank_account_id"`
	Token         string `form:"token" json:"token"`
	Chain         string `form:"chain" json:"chain"`
	Amount        string `form:"amount" json:"amount"`
}

func (r *SimulatePayoutDepositRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"rail":            payoutRailRule,
		"bank_account_id": "required|numeric",
		"token":           payoutTokenRule,
		"chain":           chainRule,
		// A decimal of up to six places, the token's precision. No `|` inside
		// the pattern: it is the rule separator.
		"amount": "required|regex:^\\d+(\\.\\d{1,6})?$",
	}
}
