package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type UpdateAddressRequest struct {
	Label          *string `form:"label"            json:"label"`
	ExternalUserID *string `form:"external_user_id" json:"external_user_id"`
}

func (r *UpdateAddressRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateAddressRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"label":            "max_len:255",
		"external_user_id": "max_len:255",
	}
}
