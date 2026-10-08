package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type UpdateAccountRequest struct {
	Name           string `form:"name"             json:"name,omitempty"`
	ViewAllWallets *bool  `form:"view_all_wallets" json:"view_all_wallets,omitempty"`
}

func (r *UpdateAccountRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateAccountRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{}
}
