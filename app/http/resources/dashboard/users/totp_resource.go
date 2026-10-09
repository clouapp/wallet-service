package users

import (
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// TOTPSecret is POST /v1/users/me/totp/setup: the secret to enter in an
// authenticator and its QR URL, shown once. The keys keep the order the
// answer has always had.
type TOTPSecret struct {
	QRURL  string `json:"qr_url"`
	Secret string `json:"secret"`
}

// NewTOTPSecret shapes the secret TOTP setup generated.
func NewTOTPSecret(setup authsvc.TOTPSecret) TOTPSecret {
	return TOTPSecret{QRURL: setup.QRURL, Secret: setup.Secret}
}

// TOTPConfirmed is POST /v1/users/me/totp/verify: the user with 2FA on and the
// recovery codes, shown once. The keys keep the order the answer has always had.
type TOTPConfirmed struct {
	RecoveryCodes []string `json:"recovery_codes"`
	User          *User    `json:"user"`
}

// NewTOTPConfirmed shapes a confirmed enrollment.
func NewTOTPConfirmed(confirmed authsvc.TOTPConfirmed) TOTPConfirmed {
	return TOTPConfirmed{RecoveryCodes: confirmed.RecoveryCodes, User: UserFrom(confirmed.User)}
}

// TOTPDisabled is DELETE /v1/users/me/totp: the user with 2FA off and the
// caller's new session. The keys keep the order the answer has always had.
type TOTPDisabled struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         *User  `json:"user"`
}

// NewTOTPDisabled shapes a disabled enrollment.
func NewTOTPDisabled(disabled authsvc.TOTPDisabled) TOTPDisabled {
	return TOTPDisabled{
		AccessToken:  disabled.Tokens.AccessToken,
		RefreshToken: disabled.Tokens.RefreshToken,
		User:         UserFrom(disabled.User),
	}
}
