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
	"github.com/macrowallets/waas/app/repositories"
)

const inviteLifetime = 72 * time.Hour

var (
	ErrInviteInvalid       = errors.New("invite is invalid or expired")
	ErrInviteLoginRequired = errors.New("log in with the invited email to accept")
	ErrInvitePassword      = errors.New("password must be at least 8 characters")
)

type IssuedInvite struct {
	Invite     *models.AccountInvite
	InviteLink string
	RawToken   string
}

func (s *Service) invites() repositories.AccountInviteRepository {
	if s != nil && s.inviteRepo != nil {
		return s.inviteRepo
	}
	return repositories.NewAccountInviteRepository()
}

// IssueInvite stores only the hash of a fresh token. It does not create a user.
func (s *Service) IssueInvite(ctx context.Context, accountID uuid.UUID, email, role string, invitedBy uuid.UUID, frontendBase string) (*IssuedInvite, error) {
	if s == nil || s.accountUserRepo == nil {
		return nil, errors.New("account user repository is required")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !models.IsAccountRole(role) {
		return nil, policies.ErrRoleAbove
	}
	actor, err := s.accountUserRepo.FindByAccountAndUser(accountID, invitedBy)
	if err != nil || actor == nil || !policies.MayGrant(actor.Role, role) {
		if err != nil {
			return nil, err
		}
		return nil, policies.ErrRoleAbove
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
	pending, findErr := s.invites().FindPendingByAccountEmail(accountID, email)
	if pending == nil && findErr != nil && !strings.Contains(strings.ToLower(findErr.Error()), "not found") {
		return nil, findErr
	}
	if pending != nil {
		if err := s.invites().Rotate(pending.ID, hash, role, expires); err != nil {
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
	if err := s.invites().Create(invite); err != nil {
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
