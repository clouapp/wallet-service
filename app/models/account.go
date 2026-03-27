package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Account struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	Name            string     `gorm:"type:varchar(255);not null" json:"name"`
	Status          string     `gorm:"type:varchar(20);default:active" json:"status"`
	ViewAllWallets  bool       `gorm:"default:false" json:"view_all_wallets"`
	Environment     string     `gorm:"type:varchar(4);default:prod" json:"environment"`
	LinkedAccountID *uuid.UUID `gorm:"type:uuid" json:"linked_account_id,omitempty"`
}

func (a *Account) TableName() string { return "accounts" }
