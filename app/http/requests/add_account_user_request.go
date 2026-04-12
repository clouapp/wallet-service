package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type AddAccountUserRequest struct {
	Email string `form:"email" json:"email"`
	Role  string `form:"role"  json:"role"`
}

func (r *AddAccountUserRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *AddAccountUserRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "trim",
	}
}

func (r *AddAccountUserRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "required|email",
		"role":  "required|in:owner,admin,viewer",
	}
}
