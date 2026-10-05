package accounts

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AccountUser is the membership row the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps and the nested user
// pointer. A nil page stays nil; an empty page stays empty. A nil membership
// stays null.
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
	User      *models.User     `json:"user,omitempty"`
}

// AccountUserFrom projects one membership.
func AccountUserFrom(member models.AccountUser) AccountUser {
	return AccountUser{
		CreatedAt: member.CreatedAt,
		UpdatedAt: member.UpdatedAt,
		ID:        member.ID,
		AccountID: member.AccountID,
		UserID:    member.UserID,
		Role:      member.Role,
		Status:    member.Status,
		AddedBy:   member.AddedBy,
		DeletedAt: member.DeletedAt,
		User:      member.User,
	}
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
