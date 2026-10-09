package addresses

import (
	"github.com/goravel/framework/contracts/http"
)

// StoreRequest is the body of a deposit address generation. The passphrase is
// only read for wallets whose keys derive from it.
type StoreRequest struct {
	ExternalUserID string `form:"external_user_id" json:"external_user_id"`
	Metadata       string `form:"metadata"         json:"metadata"`
	Label          string `form:"label"            json:"label"`
	Passphrase     string `form:"passphrase"       json:"passphrase"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"label":            "max_len:255",
		"external_user_id": "max_len:255",
		"metadata":         "max_len:4096",
		"passphrase":       "max_len:255",
	}
}
