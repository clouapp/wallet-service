package whitelist

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/requests"
)

// StoreRequest is the body of a whitelist entry: an address valid on the
// wallet's chain, and an optional label.
type StoreRequest struct {
	Address string `form:"address" json:"address"`
	Label   string `form:"label"   json:"label,omitempty"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"address": "trim",
		"label":   "trim",
	}
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"address": "required|blockchain_address",
	}
}

func (r *StoreRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	return requests.PrepareWalletChain(ctx, data)
}
