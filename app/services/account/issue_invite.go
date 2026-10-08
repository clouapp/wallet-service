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
// MailErr is set when the row was stored and the credential-mail job failed.
// The invite still stands. The link is the one the job minted when that call
// returned one.
type IssuedInvite struct {
	Invite     *models.AccountInvite
	InviteLink string
	RawToken   string
	MailErr    error
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
	s.enqueueInviteMail(issued)
	return issued, nil
}

// ResendInvite rotates the token of one open invite. The stored role and
// expiry stay. An accepted, revoked, or other-account invite is
// ErrInviteInvalid. The raw token is only on the returned link. This does
// not write member.invited: that event is the original invite.
func (s *Service) ResendInvite(ctx context.Context, accountID, inviteID uuid.UUID, frontendBase string) (*IssuedInvite, error) {
	if s == nil || s.invites == nil {
		return nil, errors.New("account invite stores are required")
	}
	if ctx == nil {
		return nil, errors.New("resend invite: context is required")
	}
	if accountID == uuid.Nil || inviteID == uuid.Nil {
		return nil, ErrInviteInvalid
	}
	invite, err := s.invites.FindOpenByAccountAndID(ctx, accountID, inviteID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	if invite == nil {
		return nil, ErrInviteInvalid
	}
	raw, hash, err := newInviteToken()
	if err != nil {
		return nil, err
	}
	link, err := InviteLink(frontendBase, raw)
	if err != nil {
		return nil, err
	}
	if err := s.invites.Rotate(ctx, invite.ID, hash, invite.Role, invite.ExpiresAt); err != nil {
		return nil, err
	}
	invite.TokenHash = hash
	issued := &IssuedInvite{Invite: invite, InviteLink: link, RawToken: raw}
	s.enqueueInviteMail(issued)
	return issued, nil
}

// enqueueInviteMail dispatches the credential-mail job after the invite row
// is stored. A nil port does not enqueue. The queue payload is the invite id.
func (s *Service) enqueueInviteMail(issued *IssuedInvite) {
	if s == nil || issued == nil || issued.Invite == nil || s.inviteMail == nil {
		return
	}
	link, err := s.inviteMail.DispatchAccountInvite(issued.Invite.ID)
	if link != "" {
		issued.InviteLink = link
	}
	issued.MailErr = err
}

// RevokeInvite stamps revoked_at on one open invite. The stored token hash,
// role and expiry stay. An accepted, revoked, unknown, or other-account
// invite is ErrInviteInvalid and the row is unchanged. The audit plugin
// records the allowlisted columns as updated. This does not send mail.
func (s *Service) RevokeInvite(ctx context.Context, accountID, inviteID uuid.UUID) error {
	if s == nil || s.invites == nil {
		return errors.New("account invite stores are required")
	}
	if ctx == nil {
		return errors.New("revoke invite: context is required")
	}
	if accountID == uuid.Nil || inviteID == uuid.Nil {
		return ErrInviteInvalid
	}
	invite, err := s.invites.FindOpenByAccountAndID(ctx, accountID, inviteID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return ErrInviteInvalid
		}
		return err
	}
	if invite == nil {
		return ErrInviteInvalid
	}
	if err := s.invites.MarkRevoked(ctx, accountID, invite.ID, time.Now()); err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return ErrInviteInvalid
		}
		return err
	}
	return nil
}

// ListInvites pages one account's invites. The store clears the token hash
// before the rows leave the repository, so this list cannot carry it.
func (s *Service) ListInvites(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountInvite, int64, error) {
	if s == nil || s.invites == nil {
		return nil, 0, errors.New("account invite stores are required")
	}
	if ctx == nil {
		return nil, 0, errors.New("list invites: context is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, errors.New("list invites: account id is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, errors.New("list invites: pagination bounds are invalid")
	}
	return s.invites.PaginateByAccountID(ctx, accountID, limit, offset)
}

// FindOpenInvite returns one invite of the account that is not accepted and
// not revoked. A missing one is models.ErrRepositoryNotFound.
func (s *Service) FindOpenInvite(ctx context.Context, accountID, inviteID uuid.UUID) (*models.AccountInvite, error) {
	if s == nil || s.invites == nil {
		return nil, errors.New("account invite stores are required")
	}
	if ctx == nil {
		return nil, errors.New("find invite: context is required")
	}
	return s.invites.FindOpenByAccountAndID(ctx, accountID, inviteID)
}

func newInviteToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(buf)
	return raw, HashInviteToken(raw), nil
}
