package requests

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"
)

type AddWhitelistEntryRequest struct {
	Address string `form:"address" json:"address"`
	Label   string `form:"label"   json:"label,omitempty"`
}

func (r *AddWhitelistEntryRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *AddWhitelistEntryRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"address": "trim",
		"label":   "trim",
	}
}

func (r *AddWhitelistEntryRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"address": "required|blockchain_address",
	}
}

func (r *AddWhitelistEntryRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	return PrepareWalletChain(ctx, data)
}
