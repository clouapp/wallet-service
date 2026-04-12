package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type AddWalletUserRequest struct {
	UserID string `form:"user_id" json:"user_id"`
	Roles  string `form:"roles"   json:"roles"`
}

func (r *AddWalletUserRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *AddWalletUserRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"user_id": "required|uuid",
	}
}
