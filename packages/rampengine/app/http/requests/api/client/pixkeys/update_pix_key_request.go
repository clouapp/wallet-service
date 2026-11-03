package pixkeys

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/pixrules"
)

// UpdatePixKeyRequest validates PUT /api/pix-keys/{id}. Deliberately the same
// rule set as CreatePixKeyRequest — see pixkeys_request_test.go's
// TestUpdatePixKeyRequestSameRulesAsCreate.
//
// This is a full replace, not a partial patch: pix_key and pix_key_type are
// both required and may change alongside label/recipient_name/recipient_document.
type UpdatePixKeyRequest struct {
	requests.Open
	Label             string `form:"label" json:"label"`
	PixKey            string `form:"pix_key" json:"pix_key"`
	PixKeyType        string `form:"pix_key_type" json:"pix_key_type"`
	RecipientName     string `form:"recipient_name" json:"recipient_name"`
	RecipientDocument string `form:"recipient_document" json:"recipient_document"`
}

func (r *UpdatePixKeyRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"label":              "required|max_len:64",
		"pix_key":            pixrules.KeyRule,
		"pix_key_type":       pixrules.KeyTypeRule,
		"recipient_name":     "required|max_len:128",
		"recipient_document": pixrules.RecipientDocumentRule,
	}
}
