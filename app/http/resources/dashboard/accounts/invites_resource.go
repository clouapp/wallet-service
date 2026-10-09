package accounts

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// Invite is one invite a users.read member may see. The token, its hash and
// the link are not fields of this view, and nothing in it says whether the
// email already has a user.
type Invite struct {
	CreatedAt  *carbon.DateTime `json:"created_at"`
	ID         uuid.UUID        `json:"id"`
	AccountID  uuid.UUID        `json:"account_id"`
	Email      string           `json:"email"`
	Role       string           `json:"role"`
	InvitedBy  *uuid.UUID       `json:"invited_by"`
	ExpiresAt  time.Time        `json:"expires_at"`
	AcceptedAt *time.Time       `json:"accepted_at"`
	RevokedAt  *time.Time       `json:"revoked_at"`
}

// NewInvite shapes one invite. A nil invite is the zero view.
func NewInvite(invite *models.AccountInvite) Invite {
	if invite == nil {
		return Invite{}
	}
	return Invite{
		CreatedAt:  invite.CreatedAt,
		ID:         invite.ID,
		AccountID:  invite.AccountID,
		Email:      invite.Email,
		Role:       invite.Role,
		InvitedBy:  invite.InvitedBy,
		ExpiresAt:  invite.ExpiresAt,
		AcceptedAt: invite.AcceptedAt,
		RevokedAt:  invite.RevokedAt,
	}
}

// NewInvites shapes a page of invites. The page is never null.
func NewInvites(invites []models.AccountInvite) []Invite {
	views := make([]Invite, 0, len(invites))
	for i := range invites {
		views = append(views, NewInvite(&invites[i]))
	}
	return views
}

// InviteLink is the 202 of POST /v1/accounts/{accountId}/users for an email no
// user has: the invite and its one-time link. The keys keep the order the
// answer has always had.
type InviteLink struct {
	Email      string    `json:"email"`
	ExpiresAt  time.Time `json:"expires_at"`
	InviteID   uuid.UUID `json:"invite_id"`
	InviteLink string    `json:"invite_link"`
	Role       string    `json:"role"`
}

// NewInviteLink shapes an issued invite.
func NewInviteLink(issued *accountsvc.IssuedInvite) InviteLink {
	return InviteLink{
		Email:      issued.Invite.Email,
		ExpiresAt:  issued.Invite.ExpiresAt,
		InviteID:   issued.Invite.ID,
		InviteLink: issued.InviteLink,
		Role:       issued.Invite.Role,
	}
}

// InvitePreview is GET /v1/auth/invites/{token}: the public facts of a pending
// invite. The keys keep the order the answer has always had.
type InvitePreview struct {
	AccountName   string `json:"account_name"`
	Email         string `json:"email"`
	Inviter       string `json:"inviter"`
	NeedsPassword bool   `json:"needs_password"`
	Role          string `json:"role"`
}

// NewInvitePreview shapes the preview the account service read.
func NewInvitePreview(preview accountsvc.InvitePreview) InvitePreview {
	return InvitePreview{
		AccountName:   preview.AccountName,
		Email:         preview.Email,
		Inviter:       preview.Inviter,
		NeedsPassword: preview.NeedsPassword,
		Role:          preview.Role,
	}
}

// AcceptedInvite is POST /v1/auth/invites/accept: the user who is now a
// member. The keys keep the order the answer has always had.
type AcceptedInvite struct {
	Email  string    `json:"email"`
	Status string    `json:"status"`
	UserID uuid.UUID `json:"user_id"`
}

// NewAcceptedInvite shapes the user an accepted invite belongs to.
func NewAcceptedInvite(user *models.User) AcceptedInvite {
	return AcceptedInvite{Email: user.Email, Status: user.Status, UserID: user.ID}
}
