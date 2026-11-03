package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/pixrules"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// CreateOfframpRequest validates POST /api/offramps.
//
// estimate_id is required, for a reason of its own rather than the on-ramp's:
// the rail's limits are expressed in BRL and an off-ramp's BRL leg is only
// knowable through a quote, so without one the configured minimum and maximum
// could not be enforced. The on-ramp requires it too, but to hold the binding
// rate — its BRL leg is the request itself.
//
// code carries the MFA step-up. It is optional at this layer because whether
// it is needed depends on the user's own MFA setting, which the service knows
// and a form request does not.
type CreateOfframpRequest struct {
	requests.Open
	AmountToken string `form:"amount_token" json:"amount_token"`
	Token       string `form:"token" json:"token"`
	Chain       string `form:"chain" json:"chain"`
	FromAddress string `form:"from_address" json:"from_address"`
	PixKey      string `form:"pix_key" json:"pix_key"`
	PixKeyType  string `form:"pix_key_type" json:"pix_key_type"`
	// RecipientName is who the PIX key belongs to. The provider registers the
	// payout destination as its own object and requires a name for it.
	RecipientName string `form:"recipient_name" json:"recipient_name"`
	// RecipientDocument is the CPF or CNPJ, letters allowed for an alphanumeric
	// CNPJ. Required for phone, email, and
	// evp keys; optional when pix_key_type is cpf or cnpj.
	RecipientDocument string `form:"recipient_document" json:"recipient_document"`
	EstimateID        string `form:"estimate_id" json:"estimate_id"`
	Code              string `form:"code" json:"code"`
	CodeType          string `form:"code_type" json:"code_type"`
	// Rail picks the payout, as on the estimate: empty or pix_payout is a Pix
	// key, ach_payout and wire_payout a registered US bank account
	// (BankBeneficiaryID).
	Rail              string `form:"rail" json:"rail"`
	BankBeneficiaryID string `form:"bank_beneficiary_id" json:"bank_beneficiary_id"`
}

// Rules reads the rail first, as EstimateOfframpRequest does: a bank payout
// carries none of the Pix fields. Any other rail, an unknown one included,
// keeps the Pix rules, whose rail rule refuses it.
func (r *CreateOfframpRequest) Rules(ctx contractshttp.Context) map[string]string {
	if ctx != nil {
		switch rail := ctx.Request().Input("rail"); rail {
		case models.RailACHPayout, models.RailWirePayout:
			return map[string]string{
				"rail":                bankRailRule,
				"bank_beneficiary_id": "required|max_len:36",
				"amount_token":        amountTokenRule,
				"token":               bankTokenRule,
				"chain":               chainRule,
				"from_address":        "required|max_len:64",
				"estimate_id":         "required|uuid",
				"code":                "max_len:16",
				"code_type":           "in:totp,backup",
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
		"estimate_id":        "required|uuid",
		"code":               "max_len:16",
		"code_type":          "in:totp,backup",
	}
}
