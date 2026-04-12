package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type ChangePasswordRequest struct {
	CurrentPassword string `form:"current_password" json:"current_password"`
	NewPassword     string `form:"new_password"     json:"new_password"`
}

func (r *ChangePasswordRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ChangePasswordRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"current_password.required": "Current password is required",
		"new_password.required":     "New password is required",
		"new_password.min_len":      "New password must be at least 8 characters",
	}
}

func (r *ChangePasswordRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"current_password": "required",
		"new_password":     "required|min_len:8",
	}
}
