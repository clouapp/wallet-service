package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// RegisterWalletRequest validates POST /api/wallets (API contract §9): an
// address the customer in scope registers as its own. code carries the MFA
// step-up, optional here for the reason UpdateVirtualAccountRequest gives:
// whether it is needed is the user's own MFA setting, which the service
// knows.
type RegisterWalletRequest struct {
	requests.Open
	Chain    string `form:"chain" json:"chain"`
	Address  string `form:"address" json:"address"`
	Code     string `form:"code" json:"code"`
	CodeType string `form:"code_type" json:"code_type"`
}

func (r *RegisterWalletRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"chain":     chainRule,
		"address":   "required|max_len:64",
		"code":      "max_len:16",
		"code_type": "in:totp,backup",
	}
}
