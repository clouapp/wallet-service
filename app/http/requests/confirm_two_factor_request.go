package requests

import "github.com/goravel/framework/contracts/http"

type ConfirmTwoFactorRequest struct {
	Code string `form:"code" json:"code"`
}

func (r *ConfirmTwoFactorRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ConfirmTwoFactorRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"code": "required|min_len:6|max_len:6",
	}
}
