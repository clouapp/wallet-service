package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type UpdateDefaultAccountRequest struct {
	AccountID string `form:"account_id" json:"account_id"`
}

func (r *UpdateDefaultAccountRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateDefaultAccountRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"account_id": "required|uuid",
	}
}
