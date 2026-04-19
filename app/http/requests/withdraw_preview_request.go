package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type WithdrawPreviewRequest struct {
	Asset  string `form:"asset"  json:"asset"`
	Amount string `form:"amount" json:"amount"`
}

func (r *WithdrawPreviewRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *WithdrawPreviewRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"asset.required":         "Asset identifier is required",
		"amount.required":        "Amount is required",
		"amount.decimal_string":  "Amount must be a valid number",
	}
}

func (r *WithdrawPreviewRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"asset":  "required",
		"amount": "required|decimal_string",
	}
}
