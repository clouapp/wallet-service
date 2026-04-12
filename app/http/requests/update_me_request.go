package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type UpdateMeRequest struct {
	FullName string `form:"full_name" json:"full_name"`
}

func (r *UpdateMeRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateMeRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"full_name": "trim",
	}
}

func (r *UpdateMeRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{}
}
