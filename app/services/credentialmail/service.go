package credentialmail

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
)

const (
	// PurposePasswordReset is the queue purpose for a reset link.
	PurposePasswordReset = "password_reset"
	// PurposeAccountInvite is the queue purpose for an invite link.
	PurposeAccountInvite = "account_invite"

	passwordResetLifetime  = time.Hour
	passwordResetURLPrefix = "https://vault.app/reset-password?token="
)

// UserLookup loads the user a reset mail is for.
type UserLookup interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
}

// TokenIssuer mints a raw token and the hash that may be stored.
type TokenIssuer interface {
	GenerateRandomToken() (string, error)
	HashToken(raw string) string
}

// ResetWriter stores a password-reset hash.
type ResetWriter interface {
	Create(ctx context.Context, token *models.PasswordResetToken) error
}

// InviteRefresher mints a new invite token and returns the message fields.
type InviteRefresher interface {
	RefreshInviteForMail(ctx context.Context, inviteID uuid.UUID, frontendBase string) (account.InviteMail, error)
}

// Sender delivers one already-built message with Mail().Send. It must not
// enqueue the message.
type Sender interface {
	SendInvite(ctx context.Context, message account.InviteMail) error
	SendReset(ctx context.Context, to, resetLink string) error
}

// DispatchFunc enqueues one credential mail. The arguments are the subject
// id and the purpose, never the token or the link.
type DispatchFunc func(subjectID uuid.UUID, purpose string) error

// Deps is everything credential mail needs. Dispatch enqueues; the job calls
// SendPasswordReset or SendAccountInvite, which mint the token.
type Deps struct {
	Users    UserLookup
	Tokens   TokenIssuer
	Resets   ResetWriter
	Invites  InviteRefresher
	Sender   Sender
	Dispatch DispatchFunc
}

// Service mints credential mail at send time.
type Service struct {
	users    UserLookup
	tokens   TokenIssuer
	resets   ResetWriter
	invites  InviteRefresher
	sender   Sender
	dispatch DispatchFunc
}

// NewService fails fast when a dependency is missing.
func NewService(deps Deps) *Service {
	if deps.Users == nil {
		panic("credential mail: user lookup is required")
	}
	if deps.Tokens == nil {
		panic("credential mail: token issuer is required")
	}
	if deps.Resets == nil {
		panic("credential mail: reset store is required")
	}
	if deps.Invites == nil {
		panic("credential mail: invite refresher is required")
	}
	if deps.Sender == nil {
		panic("credential mail: sender is required")
	}
	if deps.Dispatch == nil {
		panic("credential mail: dispatcher is required")
	}
	return &Service{
		users:    deps.Users,
		tokens:   deps.Tokens,
		resets:   deps.Resets,
		invites:  deps.Invites,
		sender:   deps.Sender,
		dispatch: deps.Dispatch,
	}
}

// KnownPurpose reports whether purpose may be placed on the queue.
func KnownPurpose(purpose string) bool {
	switch purpose {
	case PurposePasswordReset, PurposeAccountInvite:
		return true
	default:
		return false
	}
}

// Dispatch enqueues a credential mail. The payload is the subject id and the
// purpose. The token is minted when the job runs.
func (s *Service) Dispatch(subjectID uuid.UUID, purpose string) error {
	if s == nil || s.dispatch == nil {
		return errors.New("credential mail: dispatcher is required")
	}
	if subjectID == uuid.Nil {
		return errors.New("credential mail: subject id is required")
	}
	if !KnownPurpose(purpose) {
		return errors.New("credential mail: unknown purpose")
	}
	return s.dispatch(subjectID, purpose)
}

// SendPasswordReset mints a reset token, stores only its hash, and sends the
// link. A send failure is returned without the token.
func (s *Service) SendPasswordReset(ctx context.Context, userID uuid.UUID) error {
	if s == nil || s.users == nil || s.tokens == nil || s.resets == nil || s.sender == nil {
		return errors.New("credential mail: password reset dependencies are required")
	}
	if ctx == nil {
		return errors.New("password reset mail: context is required")
	}
	if userID == uuid.Nil {
		return errors.New("password reset mail: user id is required")
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("password reset mail: load user: %w", err)
	}
	if user == nil || user.Email == "" {
		return errors.New("password reset mail: user not found")
	}
	raw, err := s.tokens.GenerateRandomToken()
	if err != nil {
		return fmt.Errorf("password reset mail: mint token: %w", err)
	}
	if raw == "" {
		return errors.New("password reset mail: minted token is empty")
	}
	hash := s.tokens.HashToken(raw)
	if hash == "" || hash == raw {
		return errors.New("password reset mail: token hash is not stored form")
	}
	token := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(passwordResetLifetime),
	}
	if err := s.resets.Create(ctx, token); err != nil {
		return fmt.Errorf("password reset mail: store token: %w", err)
	}
	if err := s.sender.SendReset(ctx, user.Email, passwordResetURLPrefix+raw); err != nil {
		return errors.New("password reset mail: send failed")
	}
	return nil
}

// SendAccountInvite mints a new invite token, stores only its hash, and sends
// the link. The link is returned so a response that already shows it can use
// the same value. A send failure still returns that link and does not include
// the token in the error.
func (s *Service) SendAccountInvite(ctx context.Context, inviteID uuid.UUID) (string, error) {
	if s == nil || s.invites == nil || s.sender == nil {
		return "", errors.New("credential mail: invite dependencies are required")
	}
	if ctx == nil {
		return "", errors.New("invite mail: context is required")
	}
	if inviteID == uuid.Nil {
		return "", errors.New("invite mail: invite id is required")
	}
	base, err := account.FrontendBase()
	if err != nil {
		return "", err
	}
	message, err := s.invites.RefreshInviteForMail(ctx, inviteID, base)
	if err != nil {
		return "", err
	}
	if message.Link == "" || message.To == "" {
		return "", errors.New("invite mail: minted message is incomplete")
	}
	if err := s.sender.SendInvite(ctx, message); err != nil {
		return message.Link, errors.New("invite mail: send failed")
	}
	return message.Link, nil
}
