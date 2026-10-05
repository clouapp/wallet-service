package accounts

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AccountUser is one row of GET /v1/platform/accounts/{id}/users and of
// POST /v1/platform/accounts/{accountId}/owners. S3.4.1 does not name fields.
// The membership keys match the account member list. The nested user matches
// GET /v1/platform/users: id, email, full_name, status, suspended_at, and
// totp_enabled. Password hashes, TOTP secrets, recovery codes, and token
// material stay off.
type AccountUser struct {
	CreatedAt *carbon.DateTime  `json:"created_at"`
	UpdatedAt *carbon.DateTime  `json:"updated_at"`
	ID        uuid.UUID         `json:"id"`
	AccountID uuid.UUID         `json:"account_id"`
	UserID    uuid.UUID         `json:"user_id"`
	Role      string            `json:"role"`
	Status    string            `json:"status"`
	AddedBy   *uuid.UUID        `json:"added_by,omitempty"`
	DeletedAt *time.Time        `json:"deleted_at,omitempty"`
	User      accountMemberUser `json:"user"`
}

// accountMemberUser is the nested user on an account-users row.
// The keys are the GET /v1/platform/users row and nothing else.
type accountMemberUser struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	Status      string    `json:"status"`
	SuspendedAt *string   `json:"suspended_at"`
	TotpEnabled bool      `json:"totp_enabled"`
}

// AccountUserFrom projects one platform account membership.
func AccountUserFrom(member models.AccountUser) AccountUser {
	user := models.User{}
	if member.User != nil {
		user = *member.User
	}
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
		User: accountMemberUser{
			ID:          user.ID,
			Email:       user.Email,
			FullName:    user.FullName,
			Status:      user.Status,
			SuspendedAt: formatAccountUserSuspendedAt(user.SuspendedAt),
			TotpEnabled: user.TotpEnabled,
		},
	}
}

// AccountUsersFrom projects a page. A nil slice becomes an empty list.
func AccountUsersFrom(rows []models.AccountUser) []AccountUser {
	views := make([]AccountUser, 0, len(rows))
	for i := range rows {
		views = append(views, AccountUserFrom(rows[i]))
	}
	return views
}

func formatAccountUserSuspendedAt(at *time.Time) *string {
	if at == nil || at.IsZero() {
		return nil
	}
	text := at.UTC().Format(time.RFC3339)
	return &text
}
