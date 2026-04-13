package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
)

type EstimateWithdrawalRequest struct {
	Amount             string `form:"amount"              json:"amount"`
	DestinationAddress string `form:"destination_address" json:"destination_address"`
}

func (r *EstimateWithdrawalRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *EstimateWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
	}
}

func (r *EstimateWithdrawalRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	walletIDStr := ctx.Request().Route("walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return nil
	}
	w, err := container.Get().WalletRepo.FindByID(walletID)
	if err != nil || w == nil {
		return nil
	}
	return data.Set("_chain", w.Chain)
}
