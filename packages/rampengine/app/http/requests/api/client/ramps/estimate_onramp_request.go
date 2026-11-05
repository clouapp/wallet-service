package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// amountBRLRule is the BRL amount format both on-ramp endpoints accept: at
// least one whole unit, at most two decimals. The rail's configured minimum is
// enforced by the service — this only rejects what is not an amount at all.
const amountBRLRule = "required|regex:^[1-9]\\d*(\\.\\d{1,2})?$"

// tokenRule and chainRule bound the crypto leg to what a user may ask for,
// shared by every request here with a crypto leg (the domain-constant
// exception in CLAUDE.md's Form Requests). SOL is accepted though no provider
// serves it: it is a legitimate ask, answered no_provider_available ("not
// yet") rather than a 422 that blames the caller. A network XIP starts serving
// becomes a models constant, named here and by the adapter that serves it in
// the same change.
const (
	tokenRule = "required|in:" + models.TargetTokenUSDC + "," + models.TargetTokenUSDT + "," + models.TargetTokenBRS + "," + models.TargetTokenSOL
	chainRule = "required|in:" + models.ChainSolana
)

// EstimateOnrampRequest validates POST /api/onramps/estimate.
type EstimateOnrampRequest struct {
	requests.Open
	AmountBRL string `form:"amount_brl" json:"amount_brl"`
	Token     string `form:"token" json:"token"`
	Chain     string `form:"chain" json:"chain"`
	Wallet    string `form:"wallet" json:"wallet"`
}

func (r *EstimateOnrampRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"amount_brl": amountBRLRule,
		"token":      tokenRule,
		"chain":      chainRule,
		// The destination is the user's own wallet: XIP never takes custody, so
		// there is no address of ours this could default to.
		"wallet": "required|max_len:64",
	}
}
