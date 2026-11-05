package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// CreateOnrampRequest validates POST /api/onramps.
//
// estimate_id is required. The provider's quote for this rail is binding and
// is what fixes the rate the user was shown, so a create without one would
// hand back a BR Code priced at whatever the rate is when it lands.
//
// There is no `code` field, unlike the off-ramp's create: an on-ramp moves
// money toward the user's own wallet and the user is the one paying, so there
// is no arbitrary destination for an MFA step-up to protect.
type CreateOnrampRequest struct {
	requests.Open
	AmountBRL  string `form:"amount_brl" json:"amount_brl"`
	Token      string `form:"token" json:"token"`
	Chain      string `form:"chain" json:"chain"`
	Wallet     string `form:"wallet" json:"wallet"`
	EstimateID string `form:"estimate_id" json:"estimate_id"`
}

func (r *CreateOnrampRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"amount_brl": amountBRLRule,
		"token":      tokenRule,
		"chain":      chainRule,
		// The destination is the user's own wallet: XIP never takes custody, so
		// there is no address of ours this could default to.
		"wallet":      "required|max_len:64",
		"estimate_id": "required|uuid",
	}
}
