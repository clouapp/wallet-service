package users

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// ResetMailDispatcher enqueues the password-reset credential job. The payload
// is the user id and the purpose. It carries no token or link.
type ResetMailDispatcher interface {
	DispatchPasswordReset(userID uuid.UUID) error
}

// RequestPasswordReset dispatches a reset mail when the address belongs to a
// user. A missing user or a lookup failure dispatches nothing.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if err := s.require(ctx, "password reset mail"); err != nil {
		return err
	}
	user, err := s.store.FindByEmail(ctx, email)
	if err != nil || user == nil || user.ID == uuid.Nil {
		return nil
	}
	if s.resetMail == nil {
		return fmt.Errorf("password reset mail: dispatcher is required")
	}
	return s.resetMail.DispatchPasswordReset(user.ID)
}

// ForgotPassword is RequestPasswordReset for the anonymous recovery route,
// whose answer is the same whatever happened, so it says nothing about the
// address: a failure is logged without the address and not returned.
func (s *Service) ForgotPassword(ctx context.Context, email string) {
	if err := s.RequestPasswordReset(ctx, email); err != nil {
		slog.Error("auth: send password reset mail failed")
	}
}
