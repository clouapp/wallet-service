package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateWalletRequest struct {
	Chain      string `form:"chain"      json:"chain"`
	Label      string `form:"label"      json:"label"`
	Passphrase string `form:"passphrase" json:"passphrase"`
}

func (r *CreateWalletRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWalletRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"chain":      "required",
		"passphrase": "required|min_len:12",
	}
}
