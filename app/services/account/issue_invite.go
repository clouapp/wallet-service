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
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
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
// The invite row and member.invited commit together. The activity metadata is
// the role. The raw token and its hash stay out of the trail.
func (s *Service) IssueInvite(ctx context.Context, accountID uuid.UUID, email, role string, invitedBy uuid.UUID, frontendBase string) (*IssuedInvite, error) {
	if s == nil || s.memberships == nil || s.invites == nil {
		return nil, errors.New("account invite stores are required")
	}
	if err := s.requireActivity(); err != nil {
		return nil, err
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
	var issued *IssuedInvite
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		pending, findErr := s.invites.FindPendingByAccountEmail(ctx, accountID, email)
		if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
			return findErr
		}
		named := audit.WithIntent(ctx, audit.Intent{Event: activitylog.ActionMemberInvited})
		var invite *models.AccountInvite
		if pending != nil {
			if err := s.invites.Rotate(named, pending.ID, hash, role, expires); err != nil {
				return err
			}
			pending.TokenHash = hash
			pending.Role = role
			pending.ExpiresAt = expires
			invite = pending
		} else {
			invite = &models.AccountInvite{
				ID:        uuid.New(),
				AccountID: accountID,
				Email:     email,
				Role:      role,
				TokenHash: hash,
				InvitedBy: &invitedBy,
				ExpiresAt: expires,
			}
			if err := s.invites.Create(named, invite); err != nil {
				return err
			}
		}
		if err := s.recordMemberInvited(ctx, accountID, invitedBy, invite); err != nil {
			return err
		}
		issued = &IssuedInvite{Invite: invite, InviteLink: link, RawToken: raw}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return issued, nil
}

func newInviteToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(buf)
	return raw, HashInviteToken(raw), nil
}
