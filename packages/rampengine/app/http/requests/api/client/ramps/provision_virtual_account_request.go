package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// ProvisionVirtualAccountRequest validates POST /api/onramps/account.
//
// There is no amount. The deposit code the user gets back is permanent and
// carries none: they type the amount in their own bank app. The currency is
// optional — brl when absent, which is what every installed build means — and
// usd asks for the US bank account. Otherwise all the request says is where
// the money should be delivered once it arrives.
type ProvisionVirtualAccountRequest struct {
	requests.Open
	SourceCurrency string `form:"source_currency" json:"source_currency"`
	Token          string `form:"token" json:"token"`
	Chain          string `form:"chain" json:"chain"`
	Wallet         string `form:"wallet" json:"wallet"`
}

func (r *ProvisionVirtualAccountRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"source_currency": sourceCurrencyRule,
		"token":           tokenRule,
		"chain":           chainRule,
		// The destination is the user's own wallet: XIP never takes custody, so
		// there is no address of ours this could default to.
		"wallet": "required|max_len:64",
	}
}
