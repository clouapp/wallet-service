package accounts

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AccountUserView is the membership row the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps and the nested user
// pointer. A nil page stays nil; an empty page stays empty. A nil membership
// stays null.
type AccountUserView struct {
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

func newAccountUserView(member models.AccountUser) AccountUserView {
	return AccountUserView{
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

func accountUserViews(members []models.AccountUser) []AccountUserView {
	if members == nil {
		return nil
	}
	views := make([]AccountUserView, len(members))
	for i := range members {
		views[i] = newAccountUserView(members[i])
	}
	return views
}

// AccountUserViewPtr keeps a nil membership as JSON null.
func AccountUserViewPtr(member *models.AccountUser) *AccountUserView {
	if member == nil {
		return nil
	}
	view := newAccountUserView(*member)
	return &view
}
