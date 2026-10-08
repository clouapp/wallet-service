package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletSyncState struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key"`
	WalletID        uuid.UUID  `gorm:"type:uuid;not null;index"`
	ChainID         string     `gorm:"type:varchar(32);not null"`
	SyncScope       string     `gorm:"type:wallet_sync_scope;not null"`
	Status          string     `gorm:"type:wallet_sync_status;not null"`
	Cursor          *string    `gorm:"type:text"`
	CursorMeta      *string    `gorm:"type:jsonb"`
	LastSyncedAt    *time.Time `gorm:"type:timestamptz"`
	LastAttemptedAt *time.Time `gorm:"type:timestamptz"`
	LastError       *string    `gorm:"type:text"`
	NextReconcileAt *time.Time `gorm:"type:timestamptz"`

	Wallet *Wallet `gorm:"foreignKey:WalletID"`
}

func (w *WalletSyncState) TableName() string {
	return "wallet_sync_states"
}
