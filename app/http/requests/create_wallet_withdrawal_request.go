package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

type CreateWalletWithdrawalRequest struct {
	Amount             string `form:"amount"              json:"amount"`
	DestinationAddress string `form:"destination_address" json:"destination_address"`
	Note               string `form:"note"                json:"note,omitempty"`
	Passphrase         string `form:"passphrase"          json:"passphrase"`
	TotpCode           string `form:"totp_code"           json:"totp_code"`
	IdempotencyKey     string `form:"idempotency_key"     json:"idempotency_key,omitempty"`
	Asset              string `form:"asset"               json:"asset,omitempty"`
}

func (r *CreateWalletWithdrawalRequest) Authorize(ctx http.Context) error {
	return nil
}

// Rules returns the validation contract for the payload.
//
// totp_code is only required for dashboard (session) callers — the
// APITokenAuth surface relies on the access token itself (and, for
// tokens with require_signature=true, HMAC signing) as the authentication
// factor. We detect dashboard callers by the presence of "user_id" in
// the request context (set by SessionAuth); APITokenAuth sets
// "account_id" instead, so the rule set falls back to the minimal
// business-required fields.
func (r *CreateWalletWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	rules := map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
		"passphrase":          "required|min_len:12",
		"idempotency_key":     "uuid",
	}
	if userID, ok := ctx.Value("user_id").(uuid.UUID); ok && userID != uuid.Nil {
		rules["totp_code"] = "required|min_len:6|max_len:6"
	}
	return rules
}

func (r *CreateWalletWithdrawalRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	walletIDStr := ctx.Request().Route("walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return nil
	}
	w, err := container.MustMake[*repositories.WalletRepository]().FindByID(ctx.Context(), walletID)
	if err != nil || w == nil {
		return nil
	}
	return data.Set("_chain", w.Chain)
}
