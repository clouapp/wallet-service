package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/walletrecords"
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
	walletIDStr := ctx.Request().Route("walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return nil
	}
	w, err := container.MustMake[*walletrecords.Wallets]().FindByID(ctx.Context(), walletID)
	if err != nil || w == nil {
		return nil
	}
	return data.Set("_chain", w.Chain)
}
