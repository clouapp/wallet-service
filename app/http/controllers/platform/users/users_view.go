package users

import (
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// platformUserView is one row of GET /v1/platform/users. S3.4.1 does not name
// fields, so the row is the smallest set the existing user profile already
// shows: id, email, full_name, status, plus suspended_at and totp_enabled.
// Password hashes, TOTP secrets, recovery codes, and session material stay off.
type platformUserView struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	Status      string    `json:"status"`
	SuspendedAt *string   `json:"suspended_at"`
	TotpEnabled bool      `json:"totp_enabled"`
}

func newPlatformUserView(user models.User) platformUserView {
	return platformUserView{
		ID:          user.ID,
		Email:       user.Email,
		FullName:    user.FullName,
		Status:      user.Status,
		SuspendedAt: formatSuspendedAt(user.SuspendedAt),
		TotpEnabled: user.TotpEnabled,
	}
}

func platformUserViews(rows []models.User) []platformUserView {
	views := make([]platformUserView, 0, len(rows))
	for i := range rows {
		views = append(views, newPlatformUserView(rows[i]))
	}
	return views
}
