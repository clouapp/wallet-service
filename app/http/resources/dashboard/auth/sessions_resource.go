package auth

import (
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// PasswordChanged is POST /v1/users/me/password: the caller's new session
// after every other one ended. The keys keep the order the answer has always
// had.
type PasswordChanged struct {
	AccessToken  string `json:"access_token"`
	Message      string `json:"message"`
	RefreshToken string `json:"refresh_token"`
}

// NewPasswordChanged shapes the session a password change issued.
func NewPasswordChanged(tokens authsvc.SessionTokens) PasswordChanged {
	return PasswordChanged{
		AccessToken:  tokens.AccessToken,
		Message:      "password updated successfully",
		RefreshToken: tokens.RefreshToken,
	}
}
