package wallets

import (
	"github.com/goravel/framework/contracts/http"
)

// StoreRequest is the body of a wallet creation, on either HTTP surface.
type StoreRequest struct {
	Chain      string `form:"chain"      json:"chain"`
	Label      string `form:"label"      json:"label"`
	Passphrase string `form:"passphrase" json:"passphrase"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"chain.required":      "Blockchain chain is required",
		"chain.db_exists":     "The specified chain is not supported",
		"label.required":      "Wallet label is required",
		"passphrase.required": "Passphrase is required",
		"passphrase.min_len":  "Passphrase must be at least 12 characters",
	}
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"chain":      "required|db_exists:chains,id",
		"label":      "required",
		"passphrase": "required|min_len:12",
	}
}
