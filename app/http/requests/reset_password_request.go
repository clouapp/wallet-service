package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type ResetPasswordRequest struct {
	Token       string `form:"token"        json:"token"`
	NewPassword string `form:"new_password" json:"new_password"`
}

func (r *ResetPasswordRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ResetPasswordRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"token":        "required",
		"new_password": "required|min_len:8",
	}
}
