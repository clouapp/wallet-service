package models

import "github.com/goravel/framework/database/orm"

// DeviceToken is a device registered to receive a user's push notifications.
//
// It has no soft delete: a row is removed because the user signed out on that
// device or because the vendor declared the token dead, and in neither case is
// there anything left to restore.
//
// UpdatedAt is when the app last registered the token. Nothing else writes the
// row and the apps register on every launch, so it also dates the session that
// registered it: a registration older than the account's sessions_revoked_at
// belongs to a session that no longer stands.
type DeviceToken struct {
	orm.Model
	UserID   uint   `gorm:"not null" json:"user_id"`
	Platform string `gorm:"size:16;not null" json:"platform"`
	Token    string `gorm:"type:text;not null" json:"-"`
	// APNsEnvironment is the APNs environment that issued an iOS token; nil on
	// Android.
	APNsEnvironment *string `gorm:"column:apns_environment;size:16" json:"apns_environment,omitempty"`
	// Locale is the language the device's notifications are written in.
	Locale string `gorm:"size:16;not null" json:"locale"`
}
