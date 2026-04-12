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
	ID             uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	Name           string     `gorm:"type:varchar(100);not null" json:"name"`
	Code           string     `gorm:"type:varchar(20);uniqueIndex;not null" json:"code"`
	Symbol         string     `gorm:"type:varchar(10)" json:"symbol"`
	Type           string     `gorm:"type:currency_type;not null" json:"type"`
	Logo           *string    `gorm:"type:varchar(500)" json:"logo,omitempty"`
	Subunits       int        `gorm:"type:int;default:2" json:"subunits"`
	CurrentPrice   float64    `gorm:"type:decimal(28,10);default:1" json:"current_price"`
	LastPrice      *float64   `gorm:"type:decimal(28,10)" json:"last_price,omitempty"`
	PriceUpdatedAt *time.Time `gorm:"type:timestamptz" json:"price_updated_at,omitempty"`
	Active         bool       `gorm:"default:false" json:"active"`
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
