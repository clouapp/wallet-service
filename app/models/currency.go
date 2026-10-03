package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	CurrencyTypeCrypto = "crypto"
	CurrencyTypeFiat   = "fiat"
)

type Currency struct {
	orm.Model
	ID             uuid.UUID  `gorm:"type:uuid;primary_key"`
	Name           string     `gorm:"type:varchar(100);not null"`
	Code           string     `gorm:"type:varchar(20);uniqueIndex;not null"`
	Symbol         string     `gorm:"type:varchar(10)"`
	Type           string     `gorm:"type:currency_type;not null"`
	Logo           *string    `gorm:"type:varchar(500)"`
	Subunits       int        `gorm:"type:int;default:2"`
	CurrentPrice   float64    `gorm:"type:decimal(28,10);default:1"`
	LastPrice      *float64   `gorm:"type:decimal(28,10)"`
	PriceUpdatedAt *time.Time `gorm:"type:timestamptz"`
	Active         bool       `gorm:"default:false"`
}

func (c *Currency) TableName() string { return "currencies" }

func (c *Currency) SetNewPrice(newPrice float64) {
	old := c.CurrentPrice
	c.LastPrice = &old
	c.CurrentPrice = newPrice
	now := time.Now()
	c.PriceUpdatedAt = &now
}

func (c *Currency) IsCrypto() bool { return c.Type == CurrencyTypeCrypto }
func (c *Currency) IsFiat() bool   { return c.Type == CurrencyTypeFiat }
