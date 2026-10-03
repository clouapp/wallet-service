package wallets

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WalletUserView is the wallet membership the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps and the nested user
// pointer. A nil page stays nil; an empty page stays empty. A nil membership
// stays null.
type WalletUserView struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
	ID        uuid.UUID        `json:"id"`
	WalletID  uuid.UUID        `json:"wallet_id"`
	UserID    uuid.UUID        `json:"user_id"`
	Roles     string           `json:"roles,omitempty"`
	Status    string           `json:"status"`
	DeletedAt *time.Time       `json:"deleted_at,omitempty"`
	User      *models.User     `json:"user,omitempty"`
}

func newWalletUserView(member models.WalletUser) WalletUserView {
	return WalletUserView{
		CreatedAt: member.CreatedAt,
		UpdatedAt: member.UpdatedAt,
		ID:        member.ID,
		WalletID:  member.WalletID,
		UserID:    member.UserID,
		Roles:     member.Roles,
		Status:    member.Status,
		DeletedAt: member.DeletedAt,
		User:      member.User,
	}
}

func walletUserViews(members []models.WalletUser) []WalletUserView {
	if members == nil {
		return nil
	}
	views := make([]WalletUserView, len(members))
	for i := range members {
		views[i] = newWalletUserView(members[i])
	}
	return views
}

func walletUserViewPtr(member *models.WalletUser) *WalletUserView {
	if member == nil {
		return nil
	}
	view := newWalletUserView(*member)
	return &view
}
