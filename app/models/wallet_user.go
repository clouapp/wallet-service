package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletUser struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	WalletID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	Roles     string     `gorm:"type:text" json:"roles,omitempty"`
	Status    string     `gorm:"type:varchar(20);default:active" json:"status"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
	User      *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (w *WalletUser) TableName() string { return "wallet_users" }
