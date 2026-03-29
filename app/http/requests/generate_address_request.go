package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type GenerateAddressRequest struct {
	ExternalUserID string `form:"external_user_id" json:"external_user_id"`
	Metadata       string `form:"metadata"         json:"metadata"`
	Label          string `form:"label"            json:"label"`
}

func (r *GenerateAddressRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *GenerateAddressRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{}
}
