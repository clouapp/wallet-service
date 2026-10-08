package users

import (
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// User is one row of GET /v1/platform/users. S3.4.1 does not name
// fields, so the row is the smallest set the existing user profile already
// shows: id, email, full_name, status, plus suspended_at and totp_enabled.
// Password hashes, TOTP secrets, recovery codes, and session material stay off.
type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	Status      string    `json:"status"`
	SuspendedAt *string   `json:"suspended_at"`
	TotpEnabled bool      `json:"totp_enabled"`
}

// UserFrom projects one platform user row.
func UserFrom(user models.User) User {
	return User{
		ID:          user.ID,
		Email:       user.Email,
		FullName:    user.FullName,
		Status:      user.Status,
		SuspendedAt: formatSuspendedAt(user.SuspendedAt),
		TotpEnabled: user.TotpEnabled,
	}
}

// UsersFrom projects a page. A nil slice becomes an empty list.
func UsersFrom(rows []models.User) []User {
	views := make([]User, 0, len(rows))
	for i := range rows {
		views = append(views, UserFrom(rows[i]))
	}
	return views
}

func formatSuspendedAt(at *time.Time) *string {
	if at == nil || at.IsZero() {
		return nil
	}
	text := at.UTC().Format(time.RFC3339)
	return &text
}
