package wallets

import (
	"github.com/goravel/framework/contracts/http"
)

// ActivateRequest is the body of a wallet activation: the code shown once when the
// wallet was created, to confirm the KeyCard was saved.
type ActivateRequest struct {
	Code string `form:"code" json:"code"`
}

func (r *ActivateRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ActivateRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"code": "required",
	}
}
