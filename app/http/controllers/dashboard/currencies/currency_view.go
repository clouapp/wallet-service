package currencies

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// CurrencyView is the currency row the dashboard reads. Field order and tags
// match the model wire, including embedded timestamps. A nil page stays nil;
// an empty page stays empty. A nil currency stays null. A non-nil empty logo
// stays "".
type CurrencyView struct {
	CreatedAt      *carbon.DateTime `json:"created_at"`
	UpdatedAt      *carbon.DateTime `json:"updated_at"`
	ID             uuid.UUID        `json:"id"`
	Name           string           `json:"name"`
	Code           string           `json:"code"`
	Symbol         string           `json:"symbol"`
	Type           string           `json:"type"`
	Logo           *string          `json:"logo,omitempty"`
	Subunits       int              `json:"subunits"`
	CurrentPrice   float64          `json:"current_price"`
	LastPrice      *float64         `json:"last_price,omitempty"`
	PriceUpdatedAt *time.Time       `json:"price_updated_at,omitempty"`
	Active         bool             `json:"active"`
}

func newCurrencyView(currency models.Currency) CurrencyView {
	return CurrencyView{
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

// CurrencyViews copies a page. A nil slice stays nil; an empty slice stays empty.
func CurrencyViews(currencies []models.Currency) []CurrencyView {
	if currencies == nil {
		return nil
	}
	views := make([]CurrencyView, len(currencies))
	for i := range currencies {
		views[i] = newCurrencyView(currencies[i])
	}
	return views
}

// CurrencyViewPtr keeps a nil currency as JSON null.
func CurrencyViewPtr(currency *models.Currency) *CurrencyView {
	if currency == nil {
		return nil
	}
	view := newCurrencyView(*currency)
	return &view
}
