package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// UpdateVirtualAccountRequest validates PUT /api/onramps/account.
//
// The destination is a property of the account rather than of an order, so
// changing wallets is an edit here and never a parameter on a deposit. The full
// destination is required: a token without its chain names no address.
//
// code carries the MFA step-up. It is optional at this layer because whether
// it is needed depends on the user's own MFA setting, which the service knows
// and a form request does not — same shape as CreateOfframpRequest.
type UpdateVirtualAccountRequest struct {
	requests.Open
	// SourceCurrency picks which of the user's accounts to edit: brl when
	// absent, or usd.
	SourceCurrency string `form:"source_currency" json:"source_currency"`
	Token          string `form:"token" json:"token"`
	Chain          string `form:"chain" json:"chain"`
	Wallet         string `form:"wallet" json:"wallet"`
	Code           string `form:"code" json:"code"`
	CodeType       string `form:"code_type" json:"code_type"`
}

func (r *UpdateVirtualAccountRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"source_currency": sourceCurrencyRule,
		"token":           tokenRule,
		"chain":           chainRule,
		"wallet":          "required|max_len:64",
		"code":            "max_len:16",
		"code_type":       "in:totp,backup",
	}
}
