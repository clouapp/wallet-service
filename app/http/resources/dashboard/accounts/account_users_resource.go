package accounts

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AccountUser is the membership row the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps. Email and FullName
// are the member's user when the row carries it (the account's member list);
// nothing else of that user is shown to the other members. A nil page stays
// nil; an empty page stays empty. A nil membership stays null.
type AccountUser struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
	ID        uuid.UUID        `json:"id"`
	AccountID uuid.UUID        `json:"account_id"`
	UserID    uuid.UUID        `json:"user_id"`
	Role      string           `json:"role"`
	Status    string           `json:"status"`
	AddedBy   *uuid.UUID       `json:"added_by,omitempty"`
	DeletedAt *time.Time       `json:"deleted_at,omitempty"`
	Email     string           `json:"email,omitempty"`
	FullName  string           `json:"full_name,omitempty"`
}

// AccountUserFrom projects one membership.
func AccountUserFrom(member models.AccountUser) AccountUser {
	view := AccountUser{
		CreatedAt: member.CreatedAt,
		UpdatedAt: member.UpdatedAt,
		ID:        member.ID,
		AccountID: member.AccountID,
		UserID:    member.UserID,
		Role:      member.Role,
		Status:    member.Status,
		AddedBy:   member.AddedBy,
		DeletedAt: member.DeletedAt,
	}
	if member.User != nil {
		view.Email = member.User.Email
		view.FullName = member.User.FullName
	}
	return view
}

// AccountUsersFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func AccountUsersFrom(members []models.AccountUser) []AccountUser {
	if members == nil {
		return nil
	}
	views := make([]AccountUser, len(members))
	for i := range members {
		views[i] = AccountUserFrom(members[i])
	}
	return views
}

// AccountUserPtr keeps a nil membership as JSON null.
func AccountUserPtr(member *models.AccountUser) *AccountUser {
	if member == nil {
		return nil
	}
	view := AccountUserFrom(*member)
	return &view
}
