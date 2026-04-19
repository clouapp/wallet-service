package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type ConsolidateRequest struct {
	Passphrase     string `form:"passphrase"      json:"passphrase"`
	Asset          string `form:"asset"           json:"asset"`
	IdempotencyKey string `form:"idempotency_key" json:"idempotency_key,omitempty"`
}

func (r *ConsolidateRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ConsolidateRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"passphrase.required": "Passphrase is required",
		"passphrase.min_len":  "Passphrase must be at least 12 characters",
		"asset.required":      "Asset identifier is required",
	}
}

func (r *ConsolidateRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"passphrase": "required|min_len:12",
		"asset":      "required",
	}
}
