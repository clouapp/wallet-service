package withdrawals

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
)

// StoreRequest is the body of POST /wallets/{walletId}/withdrawals on both
// surfaces.
type StoreRequest struct {
	Amount             string `form:"amount"              json:"amount"              example:"0.001"`
	DestinationAddress string `form:"destination_address" json:"destination_address" example:"bc1q..."`
	Note               string `form:"note"                json:"note,omitempty"      example:"Monthly payment"`
	Passphrase         string `form:"passphrase"          json:"passphrase"          example:"my-secure-wallet-passphrase"`
	TotpCode           string `form:"totp_code"           json:"totp_code"           example:"123456"`
	IdempotencyKey     string `form:"idempotency_key"     json:"idempotency_key,omitempty" example:"80571fff-8d0b-5c1e-9a3f-2b6f0f7c1a11"`
	Asset              string `form:"asset"               json:"asset,omitempty"     example:"USDT"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
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
func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	rules := map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
		"passphrase":          "required|min_len:12",
		"idempotency_key":     "uuid",
	}
	if userID, ok := requestctx.UserID(ctx); ok && userID != uuid.Nil {
		rules["totp_code"] = "required|min_len:6|max_len:6"
	}
	return rules
}

func (r *StoreRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	return requests.PrepareWalletChain(ctx, data)
}
