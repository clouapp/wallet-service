package currencies

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Currency is the currency row the dashboard reads. Field order and tags
// match the model wire, including embedded timestamps. A nil page stays nil;
// an empty page stays empty. A nil currency stays null. A non-nil empty logo
// stays "".
type Currency struct {
	CreatedAt      *carbon.DateTime    `json:"created_at"`
	UpdatedAt      *carbon.DateTime    `json:"updated_at"`
	ID             uuid.UUID           `json:"id"`
	Name           string              `json:"name"`
	Code           string              `json:"code"`
	Symbol         string              `json:"symbol"`
	Type           string              `json:"type"`
	Logo           *string             `json:"logo,omitempty"`
	Subunits       int                 `json:"subunits"`
	CurrentPrice   numeric.Decimal     `json:"current_price"`
	LastPrice      numeric.NullDecimal `json:"last_price,omitzero"`
	PriceUpdatedAt *time.Time          `json:"price_updated_at,omitempty"`
	Active         bool                `json:"active"`
}

// CurrencyFrom projects one currency.
func CurrencyFrom(currency models.Currency) Currency {
	return Currency{
		CreatedAt:      currency.CreatedAt,
		UpdatedAt:      currency.UpdatedAt,
		ID:             currency.ID,
		Name:           currency.Name,
		Code:           currency.Code,
		Symbol:         currency.Symbol,
		Type:           currency.Type,
		Logo:           currency.Logo,
		Subunits:       currency.Subunits,
		CurrentPrice:   currency.CurrentPrice,
		LastPrice:      currency.LastPrice,
		PriceUpdatedAt: currency.PriceUpdatedAt,
		Active:         currency.Active,
	}
}

// CurrenciesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func CurrenciesFrom(currencies []models.Currency) []Currency {
	if currencies == nil {
		return nil
	}
	views := make([]Currency, len(currencies))
	for i := range currencies {
		views[i] = CurrencyFrom(currencies[i])
	}
	return views
}

// CurrencyPtr keeps a nil currency as JSON null.
func CurrencyPtr(currency *models.Currency) *Currency {
	if currency == nil {
		return nil
	}
	view := CurrencyFrom(*currency)
	return &view
}
