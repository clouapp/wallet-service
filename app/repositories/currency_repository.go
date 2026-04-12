package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type PriceUpdate struct {
	CurrentPrice float64
	LastPrice    float64
}

type CurrencyRepository interface {
	Create(currency *models.Currency) error
	CreateBatch(currencies []models.Currency) error
	FindByCode(code string) (*models.Currency, error)
	FindActiveCryptos() ([]models.Currency, error)
	FindActiveFiats() ([]models.Currency, error)
	FindAllActive() ([]models.Currency, error)
	UpdatePrice(code string, currentPrice, lastPrice float64) error
	UpdatePriceBatch(updates map[string]PriceUpdate) error
	FindStale(currencyType string, staleDuration time.Duration) ([]models.Currency, error)
}

type currencyRepository struct{}

func NewCurrencyRepository() CurrencyRepository {
	return &currencyRepository{}
}

func (r *currencyRepository) Create(currency *models.Currency) error {
	if currency.ID == uuid.Nil {
		currency.ID = uuid.New()
	}
	return facades.Orm().Query().Create(currency)
}

func (r *currencyRepository) CreateBatch(currencies []models.Currency) error {
	for i := range currencies {
		if currencies[i].ID == uuid.Nil {
			currencies[i].ID = uuid.New()
		}
	}
	return facades.Orm().Query().Create(&currencies)
}

func (r *currencyRepository) FindByCode(code string) (*models.Currency, error) {
	var currency models.Currency
	err := facades.Orm().Query().Where("code = ?", code).First(&currency)
	if err != nil {
		return nil, err
	}
	if currency.ID == uuid.Nil {
		return nil, nil
	}
	return &currency, nil
}

func (r *currencyRepository) FindActiveCryptos() ([]models.Currency, error) {
	var currencies []models.Currency
	err := facades.Orm().Query().
		Where("type = ? AND active = ?", models.CurrencyTypeCrypto, true).
		Order("code").
		Find(&currencies)
	return currencies, err
}

func (r *currencyRepository) FindActiveFiats() ([]models.Currency, error) {
	var currencies []models.Currency
	err := facades.Orm().Query().
		Where("type = ? AND active = ?", models.CurrencyTypeFiat, true).
		Order("code").
		Find(&currencies)
	return currencies, err
}

func (r *currencyRepository) FindAllActive() ([]models.Currency, error) {
	var currencies []models.Currency
	err := facades.Orm().Query().
		Where("active = ?", true).
		Order("type, code").
		Find(&currencies)
	return currencies, err
}

func (r *currencyRepository) UpdatePrice(code string, currentPrice, lastPrice float64) error {
	_, err := facades.Orm().Query().
		Model(&models.Currency{}).
		Where("code = ?", code).
		Update(map[string]interface{}{
			"current_price":    currentPrice,
			"last_price":       lastPrice,
			"price_updated_at": time.Now(),
		})
	return err
}

func (r *currencyRepository) UpdatePriceBatch(updates map[string]PriceUpdate) error {
	for code, update := range updates {
		if err := r.UpdatePrice(code, update.CurrentPrice, update.LastPrice); err != nil {
			return err
		}
	}
	return nil
}

func (r *currencyRepository) FindStale(currencyType string, staleDuration time.Duration) ([]models.Currency, error) {
	var currencies []models.Currency
	cutoff := time.Now().Add(-staleDuration)
	err := facades.Orm().Query().
		Where("type = ? AND active = ? AND (price_updated_at IS NULL OR price_updated_at < ?)", currencyType, true, cutoff).
		Where("code != ?", "USD").
		Find(&currencies)
	return currencies, err
}
