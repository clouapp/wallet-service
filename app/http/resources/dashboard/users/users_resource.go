package users

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// User is the profile register, login, 2FA verify, GET and PATCH /v1/users/me,
// and the TOTP confirm and disable bodies return. These tags are the wire.
// It has no field for a secret or an internal column, so it cannot reveal one.
// A nil user stays null.
type User struct {
	CreatedAt        *carbon.DateTime `json:"created_at"`
	UpdatedAt        *carbon.DateTime `json:"updated_at"`
	ID               uuid.UUID        `json:"id"`
	Email            string           `json:"email"`
	FullName         string           `json:"full_name,omitempty"`
	TotpEnabled      bool             `json:"totp_enabled"`
	Status           string           `json:"status"`
	DefaultAccountID *uuid.UUID       `json:"default_account_id,omitempty"`
	Preferences      *Preferences     `json:"preferences,omitempty"`
}

// Preferences is the nested preferences object on User. The names match the
// users.preferences jsonb document.
type Preferences struct {
	PreferredFiatCode string `json:"preferred_fiat_code,omitempty"`
	DisplayInFiat     *bool  `json:"display_in_fiat,omitempty"`
}

// UserFrom projects a user profile. Nil stays nil so the wire stays null.
func UserFrom(user *models.User) *User {
	if user == nil {
		return nil
	}
	return &User{
		CreatedAt:        user.CreatedAt,
		UpdatedAt:        user.UpdatedAt,
		ID:               user.ID,
		Email:            user.Email,
		FullName:         user.FullName,
		TotpEnabled:      user.TotpEnabled,
		Status:           user.Status,
		DefaultAccountID: user.DefaultAccountID,
		Preferences:      preferencesFrom(user.Preferences),
	}
}

func preferencesFrom(prefs *models.UserPreferences) *Preferences {
	if prefs == nil {
		return nil
	}
	return &Preferences{
		PreferredFiatCode: prefs.PreferredFiatCode,
		DisplayInFiat:     prefs.DisplayInFiat,
	}
}
