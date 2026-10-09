package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrWrongPassword is a current password that does not match.
	ErrWrongPassword = errors.New("current password is incorrect")
	// ErrResetTokenInvalid is a reset token that matches no valid one.
	ErrResetTokenInvalid = errors.New("invalid or expired token")
	// ErrPasswordNotHashed is a new password that could not be hashed.
	ErrPasswordNotHashed = errors.New("hash the new password")
	// ErrPasswordNotSaved is a new password hash that could not be stored.
	ErrPasswordNotSaved = errors.New("store the new password")
	// ErrSessionsNotReplaced is a password change whose sessions could not be
	// replaced. The new password is stored.
	ErrSessionsNotReplaced = errors.New("replace the sessions")
	// ErrSessionsNotRevoked is a password reset whose sessions could not be
	// revoked. The new password is stored.
	ErrSessionsNotRevoked = errors.New("revoke the sessions")
)

// PasswordStore writes a user's password hash (users.Service).
type PasswordStore interface {
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
}

// ResetTokenStore reads and spends password reset tokens (sessions.PasswordResets).
type ResetTokenStore interface {
	FindValidTokens(ctx context.Context) ([]models.PasswordResetToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
}

// Credentials changes a user's password, signed in or with a reset token, and
// ends the sessions the old password opened.
type Credentials struct {
	passwords *Service
	users     PasswordStore
	resets    ResetTokenStore
	sessions  *SessionIssuer
	revoker   *SessionRevoker
}

// CredentialsDeps is everything Credentials uses. Every field is required.
type CredentialsDeps struct {
	Passwords *Service
	Users     PasswordStore
	Resets    ResetTokenStore
	Sessions  *SessionIssuer
	Revoker   *SessionRevoker
}

// NewCredentials builds the password flows from CredentialsDeps.
func NewCredentials(deps CredentialsDeps) (*Credentials, error) {
	if deps.Passwords == nil || deps.Users == nil || deps.Resets == nil || deps.Sessions == nil || deps.Revoker == nil {
		return nil, errors.New("auth: credentials: all dependencies are required")
	}
	return &Credentials{
		passwords: deps.Passwords,
		users:     deps.Users,
		resets:    deps.Resets,
		sessions:  deps.Sessions,
		revoker:   deps.Revoker,
	}, nil
}

// ChangePassword checks the signed-in user's current password, stores the new
// one and replaces every session of the user with a new one for the caller,
// signed with guard. A wrong current password is ErrWrongPassword; the other
// failures name their step.
func (c *Credentials) ChangePassword(ctx context.Context, guard SessionGuard, user *models.User, current, next string) (SessionTokens, error) {
	if !c.passwords.CheckPassword(current, user.PasswordHash) {
		return SessionTokens{}, ErrWrongPassword
	}
	if err := c.storePassword(ctx, user.ID, next); err != nil {
		return SessionTokens{}, err
	}
	tokens, err := c.sessions.Replace(ctx, guard, user.ID)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("%w: %w", ErrSessionsNotReplaced, err)
	}
	return tokens, nil
}

// ResetPassword spends a reset token: it stores the new password of the
// token's user, marks the token used and revokes every session of that user.
// A token that matches no valid one is ErrResetTokenInvalid, and so is a token
// list that could not be read, which is logged. A token that cannot be marked
// used is logged; the reset stands.
func (c *Credentials) ResetPassword(ctx context.Context, rawToken, next string) error {
	tokens, err := c.resets.FindValidTokens(ctx)
	if err != nil {
		slog.Error("auth: find reset tokens", "error", err)
	}
	var matched *models.PasswordResetToken
	for i := range tokens {
		if c.passwords.CheckToken(rawToken, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return ErrResetTokenInvalid
	}

	if err := c.storePassword(ctx, matched.UserID, next); err != nil {
		return err
	}
	if err := c.resets.MarkUsed(ctx, matched.ID); err != nil {
		slog.Error("auth: mark reset token used", "error", err)
	}
	if _, err := c.revoker.RevokeAll(ctx, matched.UserID); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionsNotRevoked, err)
	}
	return nil
}

func (c *Credentials) storePassword(ctx context.Context, userID uuid.UUID, password string) error {
	hash, err := c.passwords.HashPassword(password)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPasswordNotHashed, err)
	}
	if err := c.users.UpdatePasswordHash(ctx, userID, hash); err != nil {
		return fmt.Errorf("%w: %w", ErrPasswordNotSaved, err)
	}
	return nil
}
