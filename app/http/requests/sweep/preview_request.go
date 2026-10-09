package sweep

import (
	"github.com/goravel/framework/contracts/http"
)

// PreviewRequest is the body of a withdrawal preview: the asset and the amount in
// base units.
type PreviewRequest struct {
	Asset  string `form:"asset"  json:"asset"`
	Amount string `form:"amount" json:"amount"`
}

func (r *PreviewRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *PreviewRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"asset.required":        "Asset identifier is required",
		"amount.required":       "Amount is required",
		"amount.decimal_string": "Amount must be a valid number",
	}
}

func (r *PreviewRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"asset":  "required",
		"amount": "required|decimal_string",
	}
}
