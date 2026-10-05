package requests

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"
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
	return prepareWalletChain(ctx, data)
}
