package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
)

type CreateWithdrawalRequest struct {
	ExternalUserID string `form:"external_user_id" json:"external_user_id"`
	ToAddress      string `form:"to_address"       json:"to_address"`
	Amount         string `form:"amount"           json:"amount"`
	Asset          string `form:"asset"            json:"asset"`
	Passphrase     string `form:"passphrase"       json:"passphrase"`
	IdempotencyKey string `form:"idempotency_key"  json:"idempotency_key"`
}

func (r *CreateWithdrawalRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWithdrawalRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"external_user_id.required":   "External user ID is required",
		"to_address.required":         "Destination address is required",
		"to_address.blockchain_address": "The destination address is not valid for this chain",
		"amount.required":             "Withdrawal amount is required",
		"amount.decimal_string":       "Withdrawal amount must be a valid number",
		"asset.required":              "Asset identifier is required",
		"passphrase.required":         "Passphrase is required",
		"passphrase.min_len":          "Passphrase must be at least 12 characters",
		"idempotency_key.required":    "Idempotency key is required",
	}
}

func (r *CreateWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"external_user_id": "required",
		"to_address":       "required|blockchain_address",
		"amount":           "required|decimal_string",
		"asset":            "required",
		"passphrase":       "required|min_len:12",
		"idempotency_key":  "required",
	}
}

func (r *CreateWithdrawalRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
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
