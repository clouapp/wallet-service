package users

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	userresource "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
)

// WalletUser is the wallet membership the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps and the nested user
// pointer. A nil page stays nil; an empty page stays empty. A nil membership
// stays null.
type WalletUser struct {
	CreatedAt *carbon.DateTime   `json:"created_at"`
	UpdatedAt *carbon.DateTime   `json:"updated_at"`
	ID        uuid.UUID          `json:"id"`
	WalletID  uuid.UUID          `json:"wallet_id"`
	UserID    uuid.UUID          `json:"user_id"`
	Roles     string             `json:"roles,omitempty"`
	Status    string             `json:"status"`
	DeletedAt *time.Time         `json:"deleted_at,omitempty"`
	User      *userresource.User `json:"user,omitempty"`
}

// WalletUserFrom projects one membership.
func WalletUserFrom(member models.WalletUser) WalletUser {
	return WalletUser{
		CreatedAt: member.CreatedAt,
		UpdatedAt: member.UpdatedAt,
		ID:        member.ID,
		WalletID:  member.WalletID,
		UserID:    member.UserID,
		Roles:     member.Roles,
		Status:    member.Status,
		DeletedAt: member.DeletedAt,
		User:      userresource.UserFrom(member.User),
	}
}

// WalletUsersFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func WalletUsersFrom(members []models.WalletUser) []WalletUser {
	if members == nil {
		return nil
	}
	views := make([]WalletUser, len(members))
	for i := range members {
		views[i] = WalletUserFrom(members[i])
	}
	return views
}

// WalletUserPtr keeps a nil membership as JSON null.
func WalletUserPtr(member *models.WalletUser) *WalletUser {
	if member == nil {
		return nil
	}
	view := WalletUserFrom(*member)
	return &view
}
