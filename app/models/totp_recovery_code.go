package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type TotpRecoveryCode struct {
	orm.Model
	ID       uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	UserID   uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	CodeHash string     `gorm:"type:text;not null" json:"-"`
	UsedAt   *time.Time `json:"used_at,omitempty"`
}

func (t *TotpRecoveryCode) TableName() string { return "totp_recovery_codes" }
