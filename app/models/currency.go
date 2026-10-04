package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/numeric"
)

const (
	CurrencyTypeCrypto = "crypto"
	CurrencyTypeFiat   = "fiat"
)

type Currency struct {
	orm.Model
	ID             uuid.UUID           `gorm:"type:uuid;primary_key"`
	Name           string              `gorm:"type:varchar(100);not null"`
	Code           string              `gorm:"type:varchar(20);uniqueIndex;not null"`
	Symbol         string              `gorm:"type:varchar(10)"`
	Type           string              `gorm:"type:currency_type;not null"`
	Logo           *string             `gorm:"type:varchar(500)"`
	Subunits       int                 `gorm:"type:int;default:2"`
	CurrentPrice   numeric.Decimal     `gorm:"type:decimal(28,10);default:1"`
	LastPrice      numeric.NullDecimal `gorm:"type:decimal(28,10)"`
	PriceUpdatedAt *time.Time          `gorm:"type:timestamptz"`
	Active         bool                `gorm:"default:false"`}

func (c *Currency) TableName() string { return "currencies" }

// SetNewPrice keeps the current price as the last one and stores newPrice, fitted to
// the price column.
func (c *Currency) SetNewPrice(newPrice decimal.Decimal) error {
	fitted, err := CurrencyPriceColumn.Fit(newPrice)
	if err != nil {
		return err
	}
	if !fitted.IsPositive() {
		return fmt.Errorf("currency %s price %s: %w", c.Code, fitted.String(), numeric.ErrNotPositive)
	}
	c.LastPrice = numeric.NewNullDecimal(c.CurrentPrice.Decimal)
	c.CurrentPrice = numeric.NewDecimal(fitted)
	now := time.Now()
	c.PriceUpdatedAt = &now
	return nil
}

func (c *Currency) IsCrypto() bool { return c.Type == CurrencyTypeCrypto }
func (c *Currency) IsFiat() bool   { return c.Type == CurrencyTypeFiat }
