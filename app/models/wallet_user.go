package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletUser struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key"`
	WalletID  uuid.UUID  `gorm:"type:uuid;not null;index"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index"`
	Roles     string     `gorm:"type:text"`
	Status    string     `gorm:"type:varchar(20);default:active"`
	DeletedAt *time.Time `gorm:"index"`
	User      *User      `gorm:"foreignKey:UserID"`
}

func (w *WalletUser) TableName() string { return "wallet_users" }
