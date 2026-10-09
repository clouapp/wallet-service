package users

import "github.com/goravel/framework/contracts/http"

// DisableTotpRequest is the proof required to turn 2FA off: the current
// TOTP code, or one unused recovery code.
type DisableTotpRequest struct {
	Code         string `form:"code" json:"code"`
	RecoveryCode string `form:"recovery_code" json:"recovery_code"`
}

func (r *DisableTotpRequest) Authorize(ctx http.Context) error { return nil }

func (r *DisableTotpRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{}
}
