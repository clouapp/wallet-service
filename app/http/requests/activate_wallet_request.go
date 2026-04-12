package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type ActivateWalletRequest struct {
	Code string `form:"code" json:"code"`
}

func (r *ActivateWalletRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ActivateWalletRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"code": "required",
	}
}
