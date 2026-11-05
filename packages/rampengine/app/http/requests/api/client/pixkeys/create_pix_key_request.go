package pixkeys

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/pixrules"
)

// CreatePixKeyRequest validates POST /api/pix-keys.
//
// The pix_key_type and recipient_document rules are the exact strings the
// off-ramp create/estimate requests use (pixrules) — a key that fails here
// must also fail POST /offramps, and vice versa, or a user could save a key
// the withdraw flow then refuses.
type CreatePixKeyRequest struct {
	requests.Open
	Label             string `form:"label" json:"label"`
	PixKey            string `form:"pix_key" json:"pix_key"`
	PixKeyType        string `form:"pix_key_type" json:"pix_key_type"`
	RecipientName     string `form:"recipient_name" json:"recipient_name"`
	RecipientDocument string `form:"recipient_document" json:"recipient_document"`
}

func (r *CreatePixKeyRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"label":              "required|max_len:64",
		"pix_key":            pixrules.KeyRule,
		"pix_key_type":       pixrules.KeyTypeRule,
		"recipient_name":     "required|max_len:128",
		"recipient_document": pixrules.RecipientDocumentRule,
	}
}
