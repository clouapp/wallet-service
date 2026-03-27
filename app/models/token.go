package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Token struct {
	orm.Model
	ID              uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	ChainID         string    `gorm:"type:varchar(20);not null" json:"chain_id"`
	Symbol          string    `gorm:"type:varchar(20);not null" json:"symbol"`
	Name            string    `gorm:"type:varchar(100);not null" json:"name"`
	ContractAddress string    `gorm:"type:varchar(255);not null" json:"contract_address"`
	Decimals        int       `gorm:"not null" json:"decimals"`
	IconURL         *string   `gorm:"type:varchar(500)" json:"icon_url,omitempty"`
	Status          string    `gorm:"type:varchar(20);default:active" json:"status"`
}

func (t *Token) TableName() string { return "tokens" }
