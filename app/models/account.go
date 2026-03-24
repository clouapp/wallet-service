package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Account struct {
	orm.Model
	ID             uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Name           string    `gorm:"type:varchar(255);not null" json:"name"`
	Status         string    `gorm:"type:varchar(20);default:active" json:"status"`
	ViewAllWallets bool      `gorm:"default:false" json:"view_all_wallets"`
}

func (a *Account) TableName() string { return "accounts" }
