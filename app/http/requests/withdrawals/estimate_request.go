package withdrawals

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/requests"
)

// EstimateRequest is the body of POST /wallets/{walletId}/withdrawals/estimate.
type EstimateRequest struct {
	Amount             string `form:"amount"              json:"amount"              example:"0.001"`
	DestinationAddress string `form:"destination_address" json:"destination_address" example:"tb1q..."`
}

func (r *EstimateRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *EstimateRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
	}
}

func (r *EstimateRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	return requests.PrepareWalletChain(ctx, data)
}
