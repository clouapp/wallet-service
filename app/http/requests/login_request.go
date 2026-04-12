package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type LoginRequest struct {
	Email    string `form:"email"    json:"email"`
	Password string `form:"password" json:"password"`
}

func (r *LoginRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *LoginRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"email.required":    "Email address is required",
		"email.email":       "Please provide a valid email address",
		"password.required": "Password is required",
	}
}

func (r *LoginRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "trim",
	}
}

func (r *LoginRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email":    "required|email",
		"password": "required",
	}
}
