package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type FreezeWalletRequest struct {
	FrozenUntil string `form:"frozen_until" json:"frozen_until,omitempty"`
}

func (r *FreezeWalletRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *FreezeWalletRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"frozen_until": "rfc3339",
	}
}
