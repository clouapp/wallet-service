package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	// payoutRailRule names the rails a liquidation address pays out over.
	payoutRailRule = "required|in:" + models.RailUSDACHPayout + "," + models.RailUSDWirePayout
	// payoutTokenRule leaves SOL out: Bridge drains stablecoins to a bank, not SOL.
	payoutTokenRule = "required|in:" + models.TargetTokenUSDC + "," + models.TargetTokenUSDT
)

// ProvisionPayoutAddressRequest validates POST /api/offramps/address.
//
// Every deposit into the address is a payout. The amount is optional: the app
// sends it right before the transfer, and one below the rail's minimum after
// the fee is refused (amount_below_minimum) before any crypto leaves. The
// reference is optional and held to the rail's limit (payout_reference);
// omitting it keeps an existing address's reference.
type ProvisionPayoutAddressRequest struct {
	requests.Open
	Rail          string  `form:"rail" json:"rail"`
	BankAccountID uint    `form:"bank_account_id" json:"bank_account_id"`
	Token         string  `form:"token" json:"token"`
	Chain         string  `form:"chain" json:"chain"`
	Wallet        string  `form:"wallet" json:"wallet"`
	Reference     *string `form:"reference" json:"reference,omitempty"`
	Amount        string  `form:"amount" json:"amount,omitempty"`
}

func (r *ProvisionPayoutAddressRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"rail":            payoutRailRule,
		"bank_account_id": "required|numeric",
		"token":           payoutTokenRule,
		"chain":           chainRule,
		// The user's own wallet, where a failed payout is sent back.
		"wallet":    "required|max_len:64",
		"reference": "payout_reference",
		// The token amount about to be sent, up to six places like the
		// simulated deposit's.
		"amount": "regex:^\\d+(\\.\\d{1,6})?$",
	}
}
