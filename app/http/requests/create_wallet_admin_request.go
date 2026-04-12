package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateWalletAdminRequest struct {
	Chain             string `form:"chain"              json:"chain"`
	Label             string `form:"label"              json:"label"`
	Passphrase        string `form:"passphrase"         json:"passphrase"`
	ConfirmPassphrase string `form:"confirm_passphrase" json:"confirm_passphrase"`
}

func (r *CreateWalletAdminRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWalletAdminRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"chain.required":                  "Blockchain chain is required",
		"chain.db_exists":                 "The specified chain is not supported",
		"label.required":                  "Wallet label is required",
		"passphrase.required":             "Passphrase is required",
		"passphrase.min_len":              "Passphrase must be at least 12 characters",
		"confirm_passphrase.required":     "Passphrase confirmation is required",
		"confirm_passphrase.eq_field":     "Passphrases do not match",
	}
}

func (r *CreateWalletAdminRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"chain":              "required|db_exists:chains,id",
		"label":              "required",
		"passphrase":         "required|min_len:12",
		"confirm_passphrase": "required|eq_field:passphrase",
	}
}
