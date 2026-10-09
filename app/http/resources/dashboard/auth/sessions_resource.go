package auth

import (
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/services/account"
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

// SignInAccount is one account of a sign-in answer with the user's role on
// it. The keys keep the order the answer has always had.
type SignInAccount struct {
	Environment     string     `json:"environment"`
	ID              uuid.UUID  `json:"id"`
	LinkedAccountID *uuid.UUID `json:"linked_account_id"`
	Name            string     `json:"name"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
}

// newSignInAccounts shapes the user's accounts and picks the default one. No
// account is a null list and no default.
func newSignInAccounts(signIn account.SignInAccounts) ([]SignInAccount, *SignInAccount) {
	var accounts []SignInAccount
	var defaultAccount *SignInAccount
	for _, member := range signIn.Accounts {
		accounts = append(accounts, SignInAccount{
			Environment:     member.Account.Environment,
			ID:              member.Account.ID,
			LinkedAccountID: member.Account.LinkedAccountID,
			Name:            member.Account.Name,
			Role:            member.Role,
			Status:          member.Account.Status,
		})
	}
	for i := range accounts {
		if accounts[i].ID == signIn.DefaultID {
			defaultAccount = &accounts[i]
		}
	}
	return accounts, defaultAccount
}

// Registered is the 201 of POST /v1/auth/register: the session JWT, the new
// user and the accounts registration created, with the default one twice
// (account and account_id). The keys keep the order the answer has always had.
type Registered struct {
	AccessToken string          `json:"access_token"`
	Account     *SignInAccount  `json:"account,omitempty"`
	AccountID   *uuid.UUID      `json:"account_id,omitempty"`
	Accounts    []SignInAccount `json:"accounts"`
	User        *users.User     `json:"user"`
}

// NewRegistered shapes a registration.
func NewRegistered(registered authsvc.Registered) Registered {
	accounts, defaultAccount := newSignInAccounts(registered.Accounts)
	return Registered{
		AccessToken: registered.AccessToken,
		Account:     defaultAccount,
		AccountID:   accountID(defaultAccount),
		Accounts:    accounts,
		User:        users.UserFrom(registered.User),
	}
}

// SignedIn is the 200 of a login or a 2FA verify that issued a session: the
// tokens, the user and its accounts, with the default one twice. The keys keep
// the order the answer has always had.
type SignedIn struct {
	AccessToken  string          `json:"access_token"`
	Account      *SignInAccount  `json:"account,omitempty"`
	AccountID    *uuid.UUID      `json:"account_id,omitempty"`
	Accounts     []SignInAccount `json:"accounts"`
	RefreshToken string          `json:"refresh_token"`
	User         *users.User     `json:"user"`
}

// NewSignedIn shapes a sign-in that issued a session.
func NewSignedIn(signedIn authsvc.SignedIn) SignedIn {
	accounts, defaultAccount := newSignInAccounts(signedIn.Accounts)
	return SignedIn{
		AccessToken:  signedIn.Tokens.AccessToken,
		Account:      defaultAccount,
		AccountID:    accountID(defaultAccount),
		Accounts:     accounts,
		RefreshToken: signedIn.Tokens.RefreshToken,
		User:         users.UserFrom(signedIn.User),
	}
}

func accountID(account *SignInAccount) *uuid.UUID {
	if account == nil {
		return nil
	}
	id := account.ID
	return &id
}

// TwoFactorChallenge is the 200 of a password login for a user with 2FA: the
// challenge to complete at /v1/auth/2fa/verify instead of a session. The keys
// keep the order the answer has always had.
type TwoFactorChallenge struct {
	ChallengeToken string `json:"challenge_token"`
	ExpiresIn      int    `json:"expires_in"`
	Requires2FA    bool   `json:"requires_2fa"`
}

// NewTwoFactorChallenge shapes a 2FA challenge; expires_in is whole seconds.
func NewTwoFactorChallenge(challenge authsvc.TwoFactorChallenge) TwoFactorChallenge {
	return TwoFactorChallenge{
		ChallengeToken: challenge.Token,
		ExpiresIn:      int(challenge.ExpiresIn.Seconds()),
		Requires2FA:    true,
	}
}

// Session is POST /v1/auth/refresh: the new access and refresh tokens.
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// NewSession shapes the tokens of a session.
func NewSession(tokens authsvc.SessionTokens) Session {
	return Session{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken}
}

// Message is the {"message": ...} answer of the password recovery routes.
type Message struct {
	Message string `json:"message"`
}

// NewResetRequested is POST /v1/auth/recover: the same words whether or not
// the address is registered.
func NewResetRequested() Message {
	return Message{Message: "if that address is registered, you will receive a reset link"}
}

// NewPasswordReset is POST /v1/auth/recover/confirm once the password changed.
func NewPasswordReset() Message {
	return Message{Message: "password reset successfully"}
}
