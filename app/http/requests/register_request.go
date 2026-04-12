package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type RegisterRequest struct {
	Email            string `form:"email"             json:"email"`
	Password         string `form:"password"          json:"password"`
	FullName         string `form:"full_name"         json:"full_name"`
	OrganizationName string `form:"organization_name" json:"organization_name"`
}

func (r *RegisterRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *RegisterRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"email.required":             "Email address is required",
		"email.email":                "Please provide a valid email address",
		"email.unique":               "This email address is already registered",
		"password.required":          "Password is required",
		"password.min_len":           "Password must be at least 8 characters",
		"organization_name.required": "Organization name is required",
	}
}

func (r *RegisterRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email":             "trim",
		"full_name":         "trim",
		"organization_name": "trim",
	}
}

func (r *RegisterRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email":             "required|email|unique:users,email",
		"password":          "required|min_len:8",
		"organization_name": "required",
	}
}
