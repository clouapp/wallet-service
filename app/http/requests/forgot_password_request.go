package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type ForgotPasswordRequest struct {
	Email string `form:"email" json:"email"`
}

func (r *ForgotPasswordRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ForgotPasswordRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "trim",
	}
}

func (r *ForgotPasswordRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "required|email",
	}
}
