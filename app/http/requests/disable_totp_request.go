package requests

// DisableTotpRequest is the proof required to turn 2FA off: the current
// TOTP code, or one unused recovery code.
type DisableTotpRequest struct {
	Code         string `form:"code" json:"code"`
	RecoveryCode string `form:"recovery_code" json:"recovery_code"`
}
