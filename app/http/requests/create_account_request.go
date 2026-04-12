package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateAccountRequest struct {
	Name string `form:"name" json:"name"`
}

func (r *CreateAccountRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateAccountRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"name": "trim",
	}
}

func (r *CreateAccountRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"name": "required",
	}
}
