package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletSyncState struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	WalletID        uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ChainID         string     `gorm:"type:varchar(32);not null" json:"chain_id"`
	SyncScope       string     `gorm:"type:wallet_sync_scope;not null" json:"sync_scope"`
	Status          string     `gorm:"type:wallet_sync_status;not null" json:"status"`
	Cursor          *string    `gorm:"type:text" json:"cursor,omitempty"`
	CursorMeta      *string    `gorm:"type:jsonb" json:"cursor_meta,omitempty"`
	LastSyncedAt    *time.Time `gorm:"type:timestamptz" json:"last_synced_at,omitempty"`
	LastAttemptedAt *time.Time `gorm:"type:timestamptz" json:"last_attempted_at,omitempty"`
	LastError       *string    `gorm:"type:text" json:"last_error,omitempty"`
	NextReconcileAt *time.Time `gorm:"type:timestamptz" json:"next_reconcile_at,omitempty"`

	Wallet *Wallet `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

func (w *WalletSyncState) TableName() string {
	return "wallet_sync_states"
}
