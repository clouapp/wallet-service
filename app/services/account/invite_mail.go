package account

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// InviteMail is the in-memory content of one invite message. The link is
// minted here and is not a queue argument.
type InviteMail struct {
	To          string
	InvitedBy   string
	AccountName string
	Link        string
}

const fallbackInviterName = "your team"

// RefreshInviteForMail replaces the stored hash with a new token and returns
// the message fields. The previous raw token stops working. Expiry and role
// stay as stored. The raw token is only on the returned link.
func (s *Service) RefreshInviteForMail(ctx context.Context, inviteID uuid.UUID, frontendBase string) (InviteMail, error) {
	if s == nil || s.invites == nil || s.accounts == nil || s.users == nil {
		return InviteMail{}, errors.New("account invite stores are required")
	}
	if ctx == nil {
		return InviteMail{}, errors.New("refresh invite mail: context is required")
	}
	if inviteID == uuid.Nil {
		return InviteMail{}, ErrInviteInvalid
	}
	invite, err := s.invites.FindOpenByID(ctx, inviteID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return InviteMail{}, ErrInviteInvalid
		}
		return InviteMail{}, err
	}
	if invite == nil {
		return InviteMail{}, ErrInviteInvalid
	}
	account, err := s.accounts.FindByID(ctx, invite.AccountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return InviteMail{}, ErrInviteInvalid
		}
		return InviteMail{}, err
	}
	if account == nil {
		return InviteMail{}, ErrInviteInvalid
	}
	raw, hash, err := newInviteToken()
	if err != nil {
		return InviteMail{}, err
	}
	link, err := InviteLink(frontendBase, raw)
	if err != nil {
		return InviteMail{}, err
	}
	if err := s.invites.Rotate(ctx, invite.ID, hash, invite.Role, invite.ExpiresAt); err != nil {
		return InviteMail{}, err
	}
	return InviteMail{
		To:          invite.Email,
		InvitedBy:   s.inviterName(ctx, invite.InvitedBy),
		AccountName: account.Name,
		Link:        link,
	}, nil
}

func (s *Service) inviterName(ctx context.Context, invitedBy *uuid.UUID) string {
	if invitedBy == nil || *invitedBy == uuid.Nil {
		return fallbackInviterName
	}
	inviter, err := s.users.FindByID(ctx, *invitedBy)
	if err != nil || inviter == nil {
		return fallbackInviterName
	}
	if inviter.FullName != "" {
		return inviter.FullName
	}
	if inviter.Email != "" {
		return inviter.Email
	}
	return fallbackInviterName
}
