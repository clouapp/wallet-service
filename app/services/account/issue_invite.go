package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

const inviteLifetime = 72 * time.Hour

var (
	ErrInviteInvalid       = errors.New("invite is invalid or expired")
	ErrInviteLoginRequired = errors.New("log in with the invited email to accept")
	ErrInvitePassword      = errors.New("password must be at least 8 characters")
)

// IssuedInvite is a stored invite plus the one-time link. RawToken is not persisted.
type IssuedInvite struct {
	Invite     *models.AccountInvite
	InviteLink string
	RawToken   string
}

// IssueInvite stores only the hash of a fresh token. It does not create a user.
func (s *Service) IssueInvite(ctx context.Context, accountID uuid.UUID, email, role string, invitedBy uuid.UUID, frontendBase string) (*IssuedInvite, error) {
	if s == nil || s.memberships == nil || s.invites == nil {
		return nil, errors.New("account invite stores are required")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !models.IsAccountRole(role) {
		return nil, ErrGrantRole
	}
	actor, err := s.memberships.FindByAccountAndUser(ctx, accountID, invitedBy)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, ErrGrantRole
		}
		return nil, err
	}
	if actor == nil || !policies.MayGrant(actor.Role, role) {
		return nil, ErrGrantRole
	}
	raw, hash, err := newInviteToken()
	if err != nil {
		return nil, err
	}
	link, err := InviteLink(frontendBase, raw)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(inviteLifetime)
	pending, findErr := s.invites.FindPendingByAccountEmail(ctx, accountID, email)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return nil, findErr
	}
	if pending != nil {
		if err := s.invites.Rotate(ctx, pending.ID, hash, role, expires); err != nil {
			return nil, err
		}
		pending.TokenHash = hash
		pending.Role = role
		pending.ExpiresAt = expires
		return &IssuedInvite{Invite: pending, InviteLink: link, RawToken: raw}, nil
	}
	invite := &models.AccountInvite{
		ID:        uuid.New(),
		AccountID: accountID,
		Email:     email,
		Role:      role,
		TokenHash: hash,
		InvitedBy: &invitedBy,
		ExpiresAt: expires,
	}
	if err := s.invites.Create(ctx, invite); err != nil {
		return nil, err
	}
	return &IssuedInvite{Invite: invite, InviteLink: link, RawToken: raw}, nil
}

func newInviteToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(buf)
	return raw, HashInviteToken(raw), nil
}
