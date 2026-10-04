package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type ChainResource struct {
	orm.Model
	ID           uuid.UUID `gorm:"type:uuid;primary_key"`
	ChainID      string    `gorm:"type:varchar(20);not null"`
	Type         string    `gorm:"type:varchar(20);not null"`
	Name         string    `gorm:"type:varchar(100);not null"`
	URL          string    `gorm:"type:varchar(500);not null"`
	Description  *string   `gorm:"type:text"`
	DisplayOrder int       `gorm:"default:0"`
	Status       string    `gorm:"type:varchar(20);default:active"`
}

func (cr *ChainResource) TableName() string { return "chain_resources" }
