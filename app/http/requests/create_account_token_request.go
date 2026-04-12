package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateAccountTokenRequest struct {
	Name       string `form:"name"        json:"name"`
	ValidUntil string `form:"valid_until" json:"valid_until,omitempty"`
}

func (r *CreateAccountTokenRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateAccountTokenRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"name": "trim",
	}
}

func (r *CreateAccountTokenRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"name":        "required",
		"valid_until": "rfc3339",
	}
}
