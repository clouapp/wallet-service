package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type ChainResource struct {
	orm.Model
	ID           uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	ChainID      string    `gorm:"type:varchar(20);not null" json:"chain_id"`
	Type         string    `gorm:"type:varchar(20);not null" json:"type"`
	Name         string    `gorm:"type:varchar(100);not null" json:"name"`
	URL          string    `gorm:"type:varchar(500);not null" json:"url"`
	Description  *string   `gorm:"type:text" json:"description,omitempty"`
	DisplayOrder int       `gorm:"default:0" json:"display_order"`
	Status       string    `gorm:"type:varchar(20);default:active" json:"status"`
}

func (cr *ChainResource) TableName() string { return "chain_resources" }
