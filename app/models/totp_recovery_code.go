package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type TotpRecoveryCode struct {
	orm.Model
	ID       uuid.UUID  `gorm:"type:uuid;primary_key"`
	UserID   uuid.UUID  `gorm:"type:uuid;not null;index"`
	CodeHash string     `gorm:"type:text;not null"`
	UsedAt   *time.Time `gorm:"type:timestamptz"`
}

func (t *TotpRecoveryCode) TableName() string { return "totp_recovery_codes" }
