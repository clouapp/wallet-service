package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	// MFASubjectUsers is a dashboard user. Platform admins share this table;
	// they are not a second secret store.
	MFASubjectUsers = "users"
	// MFASubjectPlatformAdmins is a platform-admin subject in the same
	// mfa_credentials and mfa_backup_codes tables.
	MFASubjectPlatformAdmins = "platform_admins"
)

// MfaCredential is one TOTP secret for a user or a platform admin.
// Secret is sealed with the enc:v1: prefix. LastUsedCounter is the single
// replay step login and withdrawal share.
type MfaCredential struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key"`
	SubjectType     string     `gorm:"type:varchar(32);not null"`
	SubjectID       uuid.UUID  `gorm:"type:uuid;not null"`
	Secret          string     `gorm:"type:text;not null"`
	ConfirmedAt     *time.Time `gorm:"type:timestamptz"`
	LastUsedCounter int64      `gorm:"not null;default:0"`
}

func (m *MfaCredential) TableName() string { return "mfa_credentials" }

// MfaBackupCode is one hashed recovery code for the same subject as
// MfaCredential. The hash is not the TOTP secret.
type MfaBackupCode struct {
	orm.Model
	ID          uuid.UUID  `gorm:"type:uuid;primary_key"`
	SubjectType string     `gorm:"type:varchar(32);not null"`
	SubjectID   uuid.UUID  `gorm:"type:uuid;not null"`
	CodeHash    string     `gorm:"type:text;not null"`
	UsedAt      *time.Time `gorm:"type:timestamptz"`
}

func (m *MfaBackupCode) TableName() string { return "mfa_backup_codes" }
