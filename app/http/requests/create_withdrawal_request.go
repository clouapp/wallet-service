package requests

import (
	"github.com/goravel/framework/contracts/http"
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

func (r *CreateWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"external_user_id": "required",
		"to_address":       "required",
		"amount":           "required",
		"asset":            "required",
		"passphrase":       "required|min_len:12",
		"idempotency_key":  "required",
	}
}
