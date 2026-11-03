package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/pixrules"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// amountTokenRule accepts up to eighteen decimals, which is what the crypto
// column stores.
const amountTokenRule = "required|regex:^\\d+(\\.\\d{1,18})?$"

// bankTokenRule is the stablecoins a bank rail pays out of. tokenRule also
// accepts SOL, which no bank rail serves (the domain-constant exception in
// CLAUDE.md's Form Requests).
const bankTokenRule = "required|in:" + models.TargetTokenUSDC + "," + models.TargetTokenUSDT

// offrampRailRule names every rail this endpoint quotes. It accepts an empty
// rail, which is Pix.
const offrampRailRule = "in:" + models.RailPixPayout + "," + models.RailACHPayout + "," + models.RailWirePayout

// bankRailRule is the rail of a bank quote.
const bankRailRule = "required|in:" + models.RailACHPayout + "," + models.RailWirePayout

// EstimateOfframpRequest validates POST /api/offramps/estimate.
type EstimateOfframpRequest struct {
	requests.Open
	AmountToken       string `form:"amount_token" json:"amount_token"`
	Token             string `form:"token" json:"token"`
	Chain             string `form:"chain" json:"chain"`
	FromAddress       string `form:"from_address" json:"from_address"`
	PixKey            string `form:"pix_key" json:"pix_key"`
	PixKeyType        string `form:"pix_key_type" json:"pix_key_type"`
	RecipientName     string `form:"recipient_name" json:"recipient_name"`
	RecipientDocument string `form:"recipient_document" json:"recipient_document"`
	// Rail picks the payout: empty or pix_payout is a Pix key, ach_payout and
	// wire_payout a registered US bank account (BankBeneficiaryID).
	Rail              string `form:"rail" json:"rail"`
	BankBeneficiaryID string `form:"bank_beneficiary_id" json:"bank_beneficiary_id"`
	// FundingFormats declares what the app can sign: "deposit" (the default)
	// and "prepared". Unknown entries are ignored by the service.
	FundingFormats []string `form:"funding_formats" json:"funding_formats"`
}

// Rules reads the rail first: a bank payout names an account the customer
// registered, so it carries none of the Pix fields. Any other rail, an unknown
// one included, keeps the Pix rules, whose rail rule refuses it.
func (r *EstimateOfframpRequest) Rules(ctx contractshttp.Context) map[string]string {
	if ctx != nil {
		switch rail := ctx.Request().Input("rail"); rail {
		case models.RailACHPayout, models.RailWirePayout:
			return map[string]string{
				"rail": bankRailRule,
				// The external id is opaque: one that names nothing is the
				// service's 404.
				"bank_beneficiary_id": "required|max_len:36",
				"amount_token":        amountTokenRule,
				"token":               bankTokenRule,
				"chain":               chainRule,
				"from_address":        "required|max_len:64",
			}
		}
	}
	return map[string]string{
		"rail":               offrampRailRule,
		"amount_token":       amountTokenRule,
		"token":              tokenRule,
		"chain":              chainRule,
		"from_address":       "required|max_len:64",
		"pix_key":            pixrules.OfframpKeyRule,
		"pix_key_type":       pixrules.KeyTypeRule,
		"recipient_name":     "required|max_len:128",
		"recipient_document": pixrules.RecipientDocumentRule,
	}
}
