package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Token struct {
	orm.Model
	ID              uuid.UUID `gorm:"type:uuid;primary_key"`
	ChainID         string    `gorm:"type:varchar(20);not null"`
	Symbol          string    `gorm:"type:varchar(20);not null"`
	Name            string    `gorm:"type:varchar(100);not null"`
	ContractAddress string    `gorm:"type:varchar(255);not null"`
	Decimals        int       `gorm:"not null"`
	IconURL         *string   `gorm:"type:varchar(500)"`
	Status          string    `gorm:"type:varchar(20);default:active"`
}

func (t *Token) TableName() string { return "tokens" }
