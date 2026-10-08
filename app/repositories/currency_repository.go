package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/numeric"
)

// PriceUpdate is one currency's new price and the price it replaces.
type PriceUpdate struct {
	CurrentPrice decimal.Decimal
	LastPrice    decimal.Decimal
}

// CurrencyRepository persists fiat and crypto price rows.
type CurrencyRepository struct {
	db.Base
}

// NewCurrencyRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewCurrencyRepository(query orm.Query) *CurrencyRepository {
	return &CurrencyRepository{Base: db.NewBase(query)}
}

// Create inserts a currency, assigning an id when the caller left it empty.
func (r *CurrencyRepository) Create(ctx context.Context, currency *models.Currency) error {
	if currency == nil {
		return fmt.Errorf("create currency: currency is nil")
	}
	if currency.ID == uuid.Nil {
		currency.ID = uuid.New()
	}
	if err := r.Query(ctx).Create(currency); err != nil {
		return fmt.Errorf("create currency: %w", err)
	}
	return nil
}

// CreateBatch inserts currencies, assigning an id to any row that lacks one.
func (r *CurrencyRepository) CreateBatch(ctx context.Context, currencies []models.Currency) error {
	if len(currencies) == 0 {
		return nil
	}
	for i := range currencies {
		if currencies[i].ID == uuid.Nil {
			currencies[i].ID = uuid.New()
		}
	}
	if err := r.Query(ctx).Create(&currencies); err != nil {
		return fmt.Errorf("create currencies: %w", err)
	}
	return nil
}

// FindByCode returns the currency with this code, or ErrRepositoryNotFound.
func (r *CurrencyRepository) FindByCode(ctx context.Context, code string) (*models.Currency, error) {
	if code == "" {
		return nil, models.ErrRepositoryNotFound
	}
	var currency models.Currency
	if err := r.Query(ctx).Where("code = ?", code).First(&currency); err != nil {
		return nil, fmt.Errorf("find currency: %w", err)
	}
	if currency.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &currency, nil
}

// FindActiveCryptos returns active crypto currencies ordered by code.
func (r *CurrencyRepository) FindActiveCryptos(ctx context.Context) ([]models.Currency, error) {
	return r.findActiveByType(ctx, models.CurrencyTypeCrypto, "code")
}

// FindActiveFiats returns active fiat currencies ordered by code.
func (r *CurrencyRepository) FindActiveFiats(ctx context.Context) ([]models.Currency, error) {
	return r.findActiveByType(ctx, models.CurrencyTypeFiat, "code")
}

// FindAllActive returns every active currency ordered by type then code.
func (r *CurrencyRepository) FindAllActive(ctx context.Context) ([]models.Currency, error) {
	var currencies []models.Currency
	if err := r.Query(ctx).Where("active = ?", true).Order("type, code").Find(&currencies); err != nil {
		return nil, fmt.Errorf("list active currencies: %w", err)
	}
	return currencies, nil
}

// SetPrice stores a positive current price and the previous one, both fitted to
// the price column. A negative last price is refused. UpdatePrice is the same write.
func (r *CurrencyRepository) SetPrice(ctx context.Context, code string, currentPrice, lastPrice decimal.Decimal) error {
	return r.UpdatePrice(ctx, code, currentPrice, lastPrice)
}

// UpdatePrice stores a positive current price and the previous one, both fitted to
// the price column.
func (r *CurrencyRepository) UpdatePrice(ctx context.Context, code string, currentPrice, lastPrice decimal.Decimal) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("currency code is required to update a price")
	}
	current, err := models.CurrencyPriceColumn.Fit(currentPrice)
	if err != nil {
		return fmt.Errorf("currency %s current price: %w", code, err)
	}
	if !current.IsPositive() {
		return fmt.Errorf("currency %s current price %s: %w", code, current.String(), numeric.ErrNotPositive)
	}
	last, err := models.CurrencyPriceColumn.Fit(lastPrice)
	if err != nil {
		return fmt.Errorf("currency %s last price: %w", code, err)
	}
	if last.IsNegative() {
		return fmt.Errorf("currency %s last price %s: %w", code, last.String(), numeric.ErrNegative)
	}
	_, err = r.Query(ctx).Model(&models.Currency{}).Where("code = ?", code).Update(map[string]any{
		"current_price":    current,
		"last_price":       last,
		"price_updated_at": time.Now(),
	})
	if err != nil {
		return fmt.Errorf("set currency price: %w", err)
	}
	return nil
}

// SetPrices writes each code's price. A failure stops the batch.
func (r *CurrencyRepository) SetPrices(ctx context.Context, updates map[string]PriceUpdate) error {
	for code, update := range updates {
		if err := r.SetPrice(ctx, code, update.CurrentPrice, update.LastPrice); err != nil {
			return err
		}
	}
	return nil
}

// FindStale returns active currencies of currencyType, other than USD, whose price
// is missing or older than staleDuration.
func (r *CurrencyRepository) FindStale(ctx context.Context, currencyType string, staleDuration time.Duration) ([]models.Currency, error) {
	var currencies []models.Currency
	cutoff := time.Now().Add(-staleDuration)
	if err := r.Query(ctx).
		Where("type = ? AND active = ? AND (price_updated_at IS NULL OR price_updated_at < ?)", currencyType, true, cutoff).
		Where("code != ?", "USD").
		Find(&currencies); err != nil {
		return nil, fmt.Errorf("list stale currencies: %w", err)
	}
	return currencies, nil
}

func (r *CurrencyRepository) findActiveByType(ctx context.Context, currencyType, order string) ([]models.Currency, error) {
	var currencies []models.Currency
	if err := r.Query(ctx).
		Where("type = ? AND active = ?", currencyType, true).
		Order(order).
		Find(&currencies); err != nil {
		return nil, fmt.Errorf("list active %s currencies: %w", currencyType, err)
	}
	return currencies, nil
}
