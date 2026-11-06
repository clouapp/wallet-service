package swaps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
)

// ExecuteRequest accepts only the frozen quote and ephemeral authorization.
type ExecuteRequest struct {
	requests.Open
	QuoteID        string `form:"quote_id" json:"quote_id"`
	IdempotencyKey string `form:"idempotency_key" json:"idempotency_key"`
	Passphrase     string `form:"passphrase" json:"passphrase"`
	TOTP           string `form:"totp_code" json:"totp_code"`
}

func (*ExecuteRequest) Rules(http.Context) map[string]string {
	return map[string]string{"quote_id": "required|uuid", "idempotency_key": "required|uuid", "passphrase": "required|min_len:12|max_len:1024", "totp_code": "max_len:8"}
}
