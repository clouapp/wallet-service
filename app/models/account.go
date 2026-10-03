package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Account struct {
	orm.Model
	ID              uuid.UUID  `gorm:"type:uuid;primary_key"`
	Name            string     `gorm:"type:varchar(255);not null"`
	Status          string     `gorm:"type:varchar(20);default:active"`
	ViewAllWallets  bool       `gorm:"default:false"`
	Environment     string     `gorm:"type:varchar(4);default:prod"`
	LinkedAccountID *uuid.UUID `gorm:"type:uuid"`
	SweepLimits     *string    `gorm:"type:jsonb"`
}

func (a *Account) TableName() string { return "accounts" }
