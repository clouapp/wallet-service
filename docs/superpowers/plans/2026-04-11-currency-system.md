# Currency System Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Replace example commands with this repo's real tools (package manager, test runner, linter).

**Goal:** Add a `currencies` table with real-time crypto/fiat price tracking, user fiat preferences, and frontend display of wallet values in the user's preferred fiat.

**Architecture:** Single `currencies` table stores crypto (USD price) and fiat (USD rate) rows. A provider-agnostic `PriceProvider` interface enables CoinGecko (primary REST), CoinMarketCap (secondary REST), and CoinAPI (tertiary REST + WebSocket). A long-running `price:websocket` command feeds real-time crypto prices; a scheduled `price:check-update` command handles failover. User preferences live as JSONB on the `users` table. Frontend converts client-side using `amount * cryptoToUsd * usdToFiat`.

**Tech Stack:** Go 1.22, Goravel v1.17, PostgreSQL, Redis, gorilla/websocket, CoinGecko/CoinMarketCap/CoinAPI REST APIs, React 19, SWR, HeroUI, TailwindCSS 4.

---

## File Map

### Create (backend)

| File | Responsibility |
|------|---------------|
| `app/models/currency.go` | Currency ORM model + constants |
| `app/models/user_preferences.go` | UserPreferences struct + defaults |
| `app/repositories/currency_repository.go` | CurrencyRepository interface + impl |
| `app/services/price/provider.go` | PriceProvider interface |
| `app/services/price/coingecko.go` | CoinGecko REST provider |
| `app/services/price/coinmarketcap.go` | CoinMarketCap REST provider |
| `app/services/price/coinapi.go` | CoinAPI REST provider |
| `app/services/price/service.go` | Price orchestration + caching |
| `app/services/price/convert.go` | Conversion helpers |
| `app/services/price/websocket.go` | CoinAPI WebSocket client |
| `app/services/price/service_test.go` | Price service tests |
| `app/services/price/convert_test.go` | Conversion tests |
| `app/console/commands/price_websocket.go` | `price:websocket` artisan command |
| `app/console/commands/price_check_update.go` | `price:check-update` failover command |
| `app/http/controllers/currency_controller.go` | Currency API endpoints |
| `app/http/controllers/preferences_controller.go` | User preferences endpoints |
| `app/http/requests/update_preferences_request.go` | Preferences validation |
| `database/migrations/20260411000001_create_currencies_table.go` | Migration |
| `database/migrations/20260411000002_add_preferences_to_users.go` | Migration |
| `database/seeders/currency_seeder.go` | Seeder registration |
| `database/seeds/currencies.go` | Crypto + fiat seed data |

### Create (frontend)

| File | Responsibility |
|------|---------------|
| `src/types/currency.ts` | Currency + UserPreferences TypeScript types |
| `src/hooks/useCurrencies.ts` | Fetch currencies + conversion helpers |
| `src/hooks/usePreferences.ts` | User preference state + update |
| `src/components/Settings/CurrencyPreferenceTab.tsx` | Fiat selector UI |

### Modify (backend)

| File | Change |
|------|--------|
| `app/models/user.go` | Add `Preferences *UserPreferences` field |
| `app/repositories/user_repository.go` | Add `UpdatePreferences` method |
| `app/container/container.go` | Add `CurrencyRepo`, `PriceService` fields |
| `app/providers/vault_container.go` | Wire currency repo + price service + providers |
| `config/vault.go` | Add price provider API key config |
| `database/migrations/migrations.go` | Register new migrations |
| `database/seeders/database_seeder.go` | Add CurrencySeeder |
| `bootstrap/app.go` | Register new commands |
| `main.go` | Add `price_updater` Lambda mode |
| `routes/admin.go` | Add currency + preference + convert routes |
| `.env.dev.example` | Add API key env vars |

### Modify (frontend)

| File | Change |
|------|--------|
| `src/types/user.ts` | Add `preferences` to User interface |
| `src/components/Assets/AssetsByWallets.tsx` | Use preferred fiat for balance display |
| `src/components/Assets/AssetsByAssets.tsx` | Use preferred fiat for portfolio totals |
| `src/pages/dashboard/settings/index.tsx` | Add currency preference tab |

---

## Task 1: Currency Model + Migration

**Files:**
- Create: `app/models/currency.go`
- Create: `database/migrations/20260411000001_create_currencies_table.go`
- Modify: `database/migrations/migrations.go`

- [ ] **Step 1: Create the Currency model**

Create `app/models/currency.go`:

```go
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
```

- [ ] **Step 2: Create the migration**

Create `database/migrations/20260411000001_create_currencies_table.go`:

```go
package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260411000001CreateCurrenciesTable struct{}

func (r *M20260411000001CreateCurrenciesTable) Signature() string {
	return "20260411000001_create_currencies_table"
}

func (r *M20260411000001CreateCurrenciesTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TYPE currency_type AS ENUM ('crypto', 'fiat');

		CREATE TABLE IF NOT EXISTS currencies (
			id               UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
			name             VARCHAR(100)    NOT NULL,
			code             VARCHAR(20)     NOT NULL UNIQUE,
			symbol           VARCHAR(10),
			type             currency_type   NOT NULL,
			logo             VARCHAR(500),
			subunits         INT             NOT NULL DEFAULT 2,
			current_price    DECIMAL(28,10)  NOT NULL DEFAULT 1,
			last_price       DECIMAL(28,10),
			price_updated_at TIMESTAMPTZ,
			active           BOOLEAN         NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ     NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_currencies_type_active ON currencies(type, active);
		CREATE INDEX IF NOT EXISTS idx_currencies_code ON currencies(code);
	`)
	return err
}

func (r *M20260411000001CreateCurrenciesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`
		DROP TABLE IF EXISTS currencies;
		DROP TYPE IF EXISTS currency_type;
	`)
	return err
}
```

- [ ] **Step 3: Register migration in `migrations.go`**

Add to `database/migrations/migrations.go` — append to the `All()` return slice:

```go
&M20260411000001CreateCurrenciesTable{},
```

- [ ] **Step 4: Run migration**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go run . artisan migrate`
Expected: Migration runs successfully, `currencies` table created.

- [ ] **Step 5: Commit**

```bash
git add app/models/currency.go database/migrations/20260411000001_create_currencies_table.go database/migrations/migrations.go
git commit -m "feat: add Currency model and create currencies table migration"
```

---

## Task 2: User Preferences Model + Migration

**Files:**
- Create: `app/models/user_preferences.go`
- Create: `database/migrations/20260411000002_add_preferences_to_users.go`
- Modify: `app/models/user.go`
- Modify: `database/migrations/migrations.go`

- [ ] **Step 1: Create UserPreferences struct**

Create `app/models/user_preferences.go`:

```go
package models

type UserPreferences struct {
	PreferredFiatCode string `json:"preferred_fiat_code,omitempty"`
	DisplayInFiat     *bool  `json:"display_in_fiat,omitempty"`
}

func (p *UserPreferences) GetPreferredFiat() string {
	if p == nil || p.PreferredFiatCode == "" {
		return "USD"
	}
	return p.PreferredFiatCode
}

func (p *UserPreferences) IsDisplayInFiat() bool {
	if p == nil || p.DisplayInFiat == nil {
		return true
	}
	return *p.DisplayInFiat
}
```

- [ ] **Step 2: Add Preferences field to User model**

In `app/models/user.go`, add the field to the `User` struct after `DefaultAccountID`:

```go
Preferences *UserPreferences `gorm:"type:jsonb;serializer:json" json:"preferences,omitempty"`
```

- [ ] **Step 3: Create the migration**

Create `database/migrations/20260411000002_add_preferences_to_users.go`:

```go
package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260411000002AddPreferencesToUsers struct{}

func (r *M20260411000002AddPreferencesToUsers) Signature() string {
	return "20260411000002_add_preferences_to_users"
}

func (r *M20260411000002AddPreferencesToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE users ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL DEFAULT '{}';
	`)
	return err
}

func (r *M20260411000002AddPreferencesToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE users DROP COLUMN IF EXISTS preferences;
	`)
	return err
}
```

- [ ] **Step 4: Register migration in `migrations.go`**

Add to `database/migrations/migrations.go` — append after the currencies migration:

```go
&M20260411000002AddPreferencesToUsers{},
```

- [ ] **Step 5: Run migration**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go run . artisan migrate`
Expected: `preferences` column added to `users` table.

- [ ] **Step 6: Commit**

```bash
git add app/models/user_preferences.go app/models/user.go database/migrations/20260411000002_add_preferences_to_users.go database/migrations/migrations.go
git commit -m "feat: add UserPreferences JSONB on users table"
```

---

## Task 3: Currency Repository

**Files:**
- Create: `app/repositories/currency_repository.go`

- [ ] **Step 1: Create CurrencyRepository**

Create `app/repositories/currency_repository.go`:

```go
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
```

- [ ] **Step 2: Extend UserRepository**

Add to `app/repositories/user_repository.go` — add to the `UserRepository` interface:

```go
UpdatePreferences(id uuid.UUID, prefs *models.UserPreferences) error
```

Add the implementation:

```go
func (r *userRepository) UpdatePreferences(id uuid.UUID, prefs *models.UserPreferences) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("preferences", prefs)
	return err
}
```

- [ ] **Step 3: Commit**

```bash
git add app/repositories/currency_repository.go app/repositories/user_repository.go
git commit -m "feat: add CurrencyRepository and UserRepository.UpdatePreferences"
```

---

## Task 4: Currency Seeds

**Files:**
- Create: `database/seeds/currencies.go`
- Create: `database/seeders/currency_seeder.go`
- Modify: `database/seeders/database_seeder.go`

- [ ] **Step 1: Create currency seed data**

Create `database/seeds/currencies.go`. This file contains two functions: `SeedCryptoCurrencies` and `SeedFiatCurrencies`. The crypto list matches our supported chains. The fiat list is the full world currency set from gamba, with ~21 marked active.

```go
package seeds

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type currencySeed struct {
	Name     string
	Code     string
	Symbol   string
	Subunits int
	Active   bool
	Logo     string
}

var activeFiatCodes = map[string]bool{
	"USD": true, "EUR": true, "BRL": true, "GBP": true, "JPY": true,
	"CAD": true, "AUD": true, "CHF": true, "CNY": true, "INR": true,
	"IDR": true, "KRW": true, "MXN": true, "DKK": true, "NZD": true,
	"PHP": true, "RUB": true, "PEN": true, "PLN": true, "VND": true,
	"TRY": true, "ARS": true, "NGN": true,
}

func SeedCurrencies(_ context.Context) error {
	if err := seedCryptos(); err != nil {
		return err
	}
	return seedFiats()
}

func seedCryptos() error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	cryptos := []currencySeed{
		{"Bitcoin", "BTC", "₿", 8, true, cmcBase + "/1.png"},
		{"Ethereum", "ETH", "Ξ", 18, true, cmcBase + "/1027.png"},
		{"Solana", "SOL", "◎", 9, true, cmcBase + "/5426.png"},
		{"Polygon", "MATIC", "MATIC", 18, true, cmcBase + "/3890.png"},
		{"Litecoin", "LTC", "Ł", 8, true, cmcBase + "/2.png"},
		{"Dogecoin", "DOGE", "Ð", 8, true, cmcBase + "/74.png"},
		{"Tether", "USDT", "₮", 6, true, cmcBase + "/825.png"},
		{"USD Coin", "USDC", "USDC", 6, true, cmcBase + "/3408.png"},
		{"XRP", "XRP", "✕", 6, true, cmcBase + "/52.png"},
		{"BNB", "BNB", "BNB", 18, true, cmcBase + "/1839.png"},
		{"TRON", "TRX", "TRX", 6, true, cmcBase + "/1958.png"},
		{"Cardano", "ADA", "₳", 6, true, cmcBase + "/2010.png"},
		{"Polkadot", "DOT", "DOT", 10, true, cmcBase + "/6636.png"},
		{"Chainlink", "LINK", "LINK", 18, true, cmcBase + "/1975.png"},
		{"Avalanche", "AVAX", "AVAX", 18, true, cmcBase + "/5805.png"},
		{"Bitcoin Cash", "BCH", "BCH", 8, true, cmcBase + "/1831.png"},
		{"Dai", "DAI", "DAI", 18, true, cmcBase + "/4943.png"},
		{"Toncoin", "TON", "TON", 9, true, cmcBase + "/11419.png"},
		{"Shiba Inu", "SHIB", "SHIB", 18, true, cmcBase + "/5994.png"},
	}

	for _, c := range cryptos {
		var existing models.Currency
		if err := facades.Orm().Query().Where("code", c.Code).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("currency already exists, skipping", "code", c.Code)
			continue
		}
		logo := c.Logo
		cur := models.Currency{
			ID:           uuid.New(),
			Name:         c.Name,
			Code:         c.Code,
			Symbol:       c.Symbol,
			Type:         models.CurrencyTypeCrypto,
			Logo:         &logo,
			Subunits:     c.Subunits,
			CurrentPrice: 0,
			Active:       c.Active,
		}
		if err := facades.Orm().Query().Create(&cur); err != nil {
			return err
		}
		slog.Info("created crypto currency", "code", c.Code)
	}
	return nil
}

func seedFiats() error {
	fiats := []currencySeed{
		{"US Dollar", "USD", "$", 2, true, ""},
		{"Euro", "EUR", "€", 2, true, ""},
		{"Brazilian Real", "BRL", "R$", 2, true, ""},
		{"British Pound", "GBP", "£", 2, false, ""},
		{"Japanese Yen", "JPY", "¥", 0, true, ""},
		{"Canadian Dollar", "CAD", "$", 2, true, ""},
		{"Australian Dollar", "AUD", "$", 2, true, ""},
		{"Swiss Franc", "CHF", "CHF", 2, false, ""},
		{"Chinese Yuan", "CNY", "¥", 2, true, ""},
		{"Indian Rupee", "INR", "₹", 2, true, ""},
		{"Indonesian Rupiah", "IDR", "Rp", 0, true, ""},
		{"South Korean Won", "KRW", "₩", 0, true, ""},
		{"Mexican Peso", "MXN", "$", 2, true, ""},
		{"Danish Krone", "DKK", "kr", 2, true, ""},
		{"New Zealand Dollar", "NZD", "$", 2, true, ""},
		{"Philippine Peso", "PHP", "₱", 2, true, ""},
		{"Russian Ruble", "RUB", "₽", 2, true, ""},
		{"Peruvian Sol", "PEN", "S/.", 2, true, ""},
		{"Polish Zloty", "PLN", "zł", 2, true, ""},
		{"Vietnamese Dong", "VND", "₫", 0, true, ""},
		{"Turkish Lira", "TRY", "₺", 2, true, ""},
		{"Argentine Peso", "ARS", "$", 2, true, ""},
		{"Nigerian Naira", "NGN", "₦", 2, true, ""},
		{"Afghan Afghani", "AFN", "؋", 2, false, ""},
		{"Albanian Lek", "ALL", "L", 2, false, ""},
		{"Algerian Dinar", "DZD", "د.ج", 2, false, ""},
		{"Angolan Kwanza", "AOA", "Kz", 2, false, ""},
		{"Armenian Dram", "AMD", "֏", 2, false, ""},
		{"Aruban Florin", "AWG", "ƒ", 2, false, ""},
		{"Azerbaijani Manat", "AZN", "₼", 2, false, ""},
		{"Bahamian Dollar", "BSD", "$", 2, false, ""},
		{"Bahraini Dinar", "BHD", ".د.ب", 3, false, ""},
		{"Bangladeshi Taka", "BDT", "৳", 2, false, ""},
		{"Barbadian Dollar", "BBD", "$", 2, false, ""},
		{"Belarusian Ruble", "BYN", "Br", 2, false, ""},
		{"Belize Dollar", "BZD", "BZ$", 2, false, ""},
		{"Bermudian Dollar", "BMD", "$", 2, false, ""},
		{"Boliviano", "BOB", "Bs.", 2, false, ""},
		{"Bosnia Mark", "BAM", "KM", 2, false, ""},
		{"Botswana Pula", "BWP", "P", 2, false, ""},
		{"Bulgarian Lev", "BGN", "лв", 2, false, ""},
		{"Burundian Franc", "BIF", "FBu", 0, false, ""},
		{"Cambodian Riel", "KHR", "៛", 2, false, ""},
		{"Cape Verdean Escudo", "CVE", "$", 2, false, ""},
		{"Chilean Peso", "CLP", "$", 0, false, ""},
		{"Colombian Peso", "COP", "$", 2, false, ""},
		{"Comorian Franc", "KMF", "CF", 0, false, ""},
		{"Congolese Franc", "CDF", "FC", 2, false, ""},
		{"Costa Rican Colon", "CRC", "₡", 2, false, ""},
		{"Croatian Kuna", "HRK", "kn", 2, false, ""},
		{"Cuban Peso", "CUP", "₱", 2, false, ""},
		{"Czech Koruna", "CZK", "Kč", 2, false, ""},
		{"Djiboutian Franc", "DJF", "Fdj", 0, false, ""},
		{"Dominican Peso", "DOP", "RD$", 2, false, ""},
		{"East Caribbean Dollar", "XCD", "$", 2, false, ""},
		{"Egyptian Pound", "EGP", "£", 2, false, ""},
		{"Eritrean Nakfa", "ERN", "Nfk", 2, false, ""},
		{"Ethiopian Birr", "ETB", "Br", 2, false, ""},
		{"Fijian Dollar", "FJD", "$", 2, false, ""},
		{"Georgian Lari", "GEL", "₾", 2, false, ""},
		{"Ghanaian Cedi", "GHS", "₵", 2, false, ""},
		{"Guatemalan Quetzal", "GTQ", "Q", 2, false, ""},
		{"Guinean Franc", "GNF", "FG", 0, false, ""},
		{"Guyanese Dollar", "GYD", "$", 2, false, ""},
		{"Haitian Gourde", "HTG", "G", 2, false, ""},
		{"Honduran Lempira", "HNL", "L", 2, false, ""},
		{"Hong Kong Dollar", "HKD", "HK$", 2, false, ""},
		{"Hungarian Forint", "HUF", "Ft", 2, false, ""},
		{"Icelandic Krona", "ISK", "kr", 0, false, ""},
		{"Iranian Rial", "IRR", "﷼", 2, false, ""},
		{"Iraqi Dinar", "IQD", "ع.د", 3, false, ""},
		{"Israeli Shekel", "ILS", "₪", 2, false, ""},
		{"Jamaican Dollar", "JMD", "J$", 2, false, ""},
		{"Jordanian Dinar", "JOD", "د.ا", 3, false, ""},
		{"Kazakhstani Tenge", "KZT", "₸", 2, false, ""},
		{"Kenyan Shilling", "KES", "KSh", 2, false, ""},
		{"Kuwaiti Dinar", "KWD", "د.ك", 3, false, ""},
		{"Kyrgyzstani Som", "KGS", "сом", 2, false, ""},
		{"Lao Kip", "LAK", "₭", 2, false, ""},
		{"Lebanese Pound", "LBP", "ل.ل", 2, false, ""},
		{"Lesotho Loti", "LSL", "L", 2, false, ""},
		{"Liberian Dollar", "LRD", "$", 2, false, ""},
		{"Libyan Dinar", "LYD", "ل.د", 3, false, ""},
		{"Macanese Pataca", "MOP", "MOP$", 2, false, ""},
		{"Malagasy Ariary", "MGA", "Ar", 2, false, ""},
		{"Malawian Kwacha", "MWK", "MK", 2, false, ""},
		{"Malaysian Ringgit", "MYR", "RM", 2, false, ""},
		{"Maldivian Rufiyaa", "MVR", "Rf", 2, false, ""},
		{"Mauritanian Ouguiya", "MRU", "UM", 2, false, ""},
		{"Mauritian Rupee", "MUR", "₨", 2, false, ""},
		{"Moldovan Leu", "MDL", "L", 2, false, ""},
		{"Mongolian Tugrik", "MNT", "₮", 2, false, ""},
		{"Moroccan Dirham", "MAD", "د.م.", 2, false, ""},
		{"Mozambican Metical", "MZN", "MT", 2, false, ""},
		{"Myanmar Kyat", "MMK", "K", 2, false, ""},
		{"Namibian Dollar", "NAD", "$", 2, false, ""},
		{"Nepalese Rupee", "NPR", "₨", 2, false, ""},
		{"Nicaraguan Cordoba", "NIO", "C$", 2, false, ""},
		{"North Korean Won", "KPW", "₩", 2, false, ""},
		{"Norwegian Krone", "NOK", "kr", 2, false, ""},
		{"Omani Rial", "OMR", "﷼", 3, false, ""},
		{"Pakistani Rupee", "PKR", "₨", 2, false, ""},
		{"Panamanian Balboa", "PAB", "B/.", 2, false, ""},
		{"Papua New Guinean Kina", "PGK", "K", 2, false, ""},
		{"Paraguayan Guarani", "PYG", "₲", 0, false, ""},
		{"Qatari Riyal", "QAR", "﷼", 2, false, ""},
		{"Romanian Leu", "RON", "lei", 2, false, ""},
		{"Rwandan Franc", "RWF", "RF", 0, false, ""},
		{"Saudi Riyal", "SAR", "﷼", 2, false, ""},
		{"Serbian Dinar", "RSD", "дин.", 2, false, ""},
		{"Seychellois Rupee", "SCR", "₨", 2, false, ""},
		{"Sierra Leonean Leone", "SLL", "Le", 2, false, ""},
		{"Singapore Dollar", "SGD", "S$", 2, false, ""},
		{"Solomon Islands Dollar", "SBD", "$", 2, false, ""},
		{"Somali Shilling", "SOS", "Sh", 2, false, ""},
		{"South African Rand", "ZAR", "R", 2, false, ""},
		{"South Sudanese Pound", "SSP", "£", 2, false, ""},
		{"Sri Lankan Rupee", "LKR", "₨", 2, false, ""},
		{"Sudanese Pound", "SDG", "ج.س.", 2, false, ""},
		{"Surinamese Dollar", "SRD", "$", 2, false, ""},
		{"Swazi Lilangeni", "SZL", "E", 2, false, ""},
		{"Swedish Krona", "SEK", "kr", 2, false, ""},
		{"Syrian Pound", "SYP", "£", 2, false, ""},
		{"Taiwan Dollar", "TWD", "NT$", 2, false, ""},
		{"Tajikistani Somoni", "TJS", "SM", 2, false, ""},
		{"Tanzanian Shilling", "TZS", "TSh", 2, false, ""},
		{"Thai Baht", "THB", "฿", 2, false, ""},
		{"Tongan Paanga", "TOP", "T$", 2, false, ""},
		{"Trinidad Dollar", "TTD", "TT$", 2, false, ""},
		{"Tunisian Dinar", "TND", "د.ت", 3, false, ""},
		{"Turkmen Manat", "TMT", "T", 2, false, ""},
		{"Ugandan Shilling", "UGX", "USh", 0, false, ""},
		{"Ukrainian Hryvnia", "UAH", "₴", 2, false, ""},
		{"UAE Dirham", "AED", "د.إ", 2, false, ""},
		{"Uruguayan Peso", "UYU", "$U", 2, false, ""},
		{"Uzbekistani Som", "UZS", "сўм", 2, false, ""},
		{"Vanuatu Vatu", "VUV", "VT", 0, false, ""},
		{"Venezuelan Bolivar", "VES", "Bs.S", 2, false, ""},
		{"West African CFA", "XOF", "CFA", 0, false, ""},
		{"Central African CFA", "XAF", "FCFA", 0, false, ""},
		{"CFP Franc", "XPF", "₣", 0, false, ""},
		{"Samoan Tala", "WST", "T", 2, false, ""},
		{"Yemeni Rial", "YER", "﷼", 2, false, ""},
		{"Zambian Kwacha", "ZMW", "ZK", 2, false, ""},
	}

	for _, f := range fiats {
		var existing models.Currency
		if err := facades.Orm().Query().Where("code", f.Code).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("currency already exists, skipping", "code", f.Code)
			continue
		}
		price := 1.0
		if f.Code == "USD" {
			price = 1.0
		} else {
			price = 0
		}
		cur := models.Currency{
			ID:           uuid.New(),
			Name:         f.Name,
			Code:         f.Code,
			Symbol:       f.Symbol,
			Type:         models.CurrencyTypeFiat,
			Subunits:     f.Subunits,
			CurrentPrice: price,
			Active:       activeFiatCodes[f.Code],
		}
		if err := facades.Orm().Query().Create(&cur); err != nil {
			return err
		}
		slog.Info("created fiat currency", "code", f.Code)
	}
	return nil
}
```

- [ ] **Step 2: Create CurrencySeeder**

Create `database/seeders/currency_seeder.go`:

```go
package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

type CurrencySeeder struct{}

func (s *CurrencySeeder) Signature() string {
	return "CurrencySeeder"
}

func (s *CurrencySeeder) Run() error {
	return seeds.SeedCurrencies(context.Background())
}
```

- [ ] **Step 3: Register in DatabaseSeeder**

In `database/seeders/database_seeder.go`, add `&CurrencySeeder{}` to the seeder list — place it before `&PairedAccountSeeder{}` since currencies have no FK dependencies:

```go
if err := facades.Seeder().Call([]seeder.Seeder{
    &ChainSeeder{},
    &TokenSeeder{},
    &ChainResourceSeeder{},
    &CurrencySeeder{},
    &PairedAccountSeeder{},
    &UserSeeder{},
    &AccountUserSeeder{},
    &WalletSeeder{},
}); err != nil {
```

- [ ] **Step 4: Run seed**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go run . artisan db:seed`
Expected: Currency rows created (crypto + fiat).

- [ ] **Step 5: Commit**

```bash
git add database/seeds/currencies.go database/seeders/currency_seeder.go database/seeders/database_seeder.go
git commit -m "feat: seed crypto and fiat currencies"
```

---

## Task 5: Price Provider Interface + CoinGecko Implementation

**Files:**
- Create: `app/services/price/provider.go`
- Create: `app/services/price/coingecko.go`

- [ ] **Step 1: Create PriceProvider interface**

Create `app/services/price/provider.go`:

```go
package price

type PriceProvider interface {
	Name() string
	FetchCryptoPrices(codes []string) (map[string]float64, error)
	FetchFiatRates(codes []string) (map[string]float64, error)
}
```

- [ ] **Step 2: Create CoinGecko provider**

Create `app/services/price/coingecko.go`:

```go
package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var geckoIDMap = map[string]string{
	"BTC": "bitcoin", "ETH": "ethereum", "SOL": "solana", "MATIC": "matic-network",
	"LTC": "litecoin", "DOGE": "dogecoin", "USDT": "tether", "USDC": "usd-coin",
	"XRP": "ripple", "BNB": "binancecoin", "TRX": "tron", "ADA": "cardano",
	"DOT": "polkadot", "LINK": "chainlink", "AVAX": "avalanche-2", "BCH": "bitcoin-cash",
	"DAI": "dai", "TON": "the-open-network", "SHIB": "shiba-inu",
}

var geckoReverseMap map[string]string

func init() {
	geckoReverseMap = make(map[string]string, len(geckoIDMap))
	for code, id := range geckoIDMap {
		geckoReverseMap[id] = code
	}
}

type CoinGeckoProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewCoinGeckoProvider(apiKey string) *CoinGeckoProvider {
	baseURL := "https://api.coingecko.com/api/v3"
	if apiKey != "" {
		baseURL = "https://pro-api.coingecko.com/api/v3"
	}
	return &CoinGeckoProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinGeckoProvider) Name() string { return "coingecko" }

func (p *CoinGeckoProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
	ids := make([]string, 0, len(codes))
	for _, code := range codes {
		if id, ok := geckoIDMap[strings.ToUpper(code)]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[string]float64{}, nil
	}

	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd", p.baseURL, strings.Join(ids, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko crypto prices: %w", err)
	}

	var result map[string]map[string]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko parse: %w", err)
	}

	prices := make(map[string]float64, len(result))
	for geckoID, data := range result {
		if code, ok := geckoReverseMap[geckoID]; ok {
			if usdPrice, exists := data["usd"]; exists && usdPrice > 0 {
				prices[code] = usdPrice
			}
		}
	}
	return prices, nil
}

func (p *CoinGeckoProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	lowerCodes := make([]string, len(codes))
	for i, c := range codes {
		lowerCodes[i] = strings.ToLower(c)
	}

	url := fmt.Sprintf("%s/simple/price?ids=usd-coin&vs_currencies=%s", p.baseURL, strings.Join(lowerCodes, ","))
	body, err := p.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("coingecko fiat rates: %w", err)
	}

	var result map[string]map[string]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coingecko fiat parse: %w", err)
	}

	rates := make(map[string]float64, len(codes))
	if usdcData, ok := result["usd-coin"]; ok {
		for _, code := range codes {
			lower := strings.ToLower(code)
			if fiatVal, exists := usdcData[lower]; exists && fiatVal > 0 {
				rates[code] = 1.0 / fiatVal
			}
		}
	}
	return rates, nil
}

func (p *CoinGeckoProvider) doGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", p.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 3: Commit**

```bash
git add app/services/price/provider.go app/services/price/coingecko.go
git commit -m "feat: add PriceProvider interface and CoinGecko implementation"
```

---

## Task 6: CoinMarketCap + CoinAPI REST Providers

**Files:**
- Create: `app/services/price/coinmarketcap.go`
- Create: `app/services/price/coinapi.go`

- [ ] **Step 1: Create CoinMarketCap provider**

Create `app/services/price/coinmarketcap.go`:

```go
package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var cmcAssetMapping = map[string]string{
	"MATIC": "MATIC",
}

type CoinMarketCapProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewCoinMarketCapProvider(apiKey string) *CoinMarketCapProvider {
	return &CoinMarketCapProvider{
		apiKey:  apiKey,
		baseURL: "https://pro-api.coinmarketcap.com",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinMarketCapProvider) Name() string { return "coinmarketcap" }

func (p *CoinMarketCapProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinmarketcap: api key not configured")
	}

	apiSymbols := make([]string, len(codes))
	reverseMap := make(map[string]string, len(codes))
	for i, code := range codes {
		upper := strings.ToUpper(code)
		apiSym := upper
		if mapped, ok := cmcAssetMapping[upper]; ok {
			apiSym = mapped
		}
		apiSymbols[i] = apiSym
		reverseMap[apiSym] = upper
	}

	url := fmt.Sprintf("%s/v1/cryptocurrency/quotes/latest?symbol=%s&convert=USD", p.baseURL, strings.Join(apiSymbols, ","))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CMC_PRO_API_KEY", p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinmarketcap: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data map[string]struct {
			Quote struct {
				USD struct {
					Price float64 `json:"price"`
				} `json:"USD"`
			} `json:"quote"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinmarketcap parse: %w", err)
	}

	prices := make(map[string]float64, len(result.Data))
	for apiSym, data := range result.Data {
		if code, ok := reverseMap[apiSym]; ok && data.Quote.USD.Price > 0 {
			prices[code] = data.Quote.USD.Price
		}
	}
	return prices, nil
}

func (p *CoinMarketCapProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	return nil, fmt.Errorf("coinmarketcap: fiat rates not supported")
}
```

- [ ] **Step 2: Create CoinAPI REST provider**

Create `app/services/price/coinapi.go`:

```go
package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var coinAPIAssetMapping = map[string]string{
	"MATIC": "POL",
}

var coinAPIReverseMapping map[string]string

func init() {
	coinAPIReverseMapping = make(map[string]string, len(coinAPIAssetMapping))
	for code, asset := range coinAPIAssetMapping {
		coinAPIReverseMapping[asset] = code
	}
}

type CoinAPIProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewCoinAPIProvider(apiKey string) *CoinAPIProvider {
	return &CoinAPIProvider{
		apiKey:  apiKey,
		baseURL: "https://rest.coinapi.io/v1",
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *CoinAPIProvider) Name() string { return "coinapi" }

func (p *CoinAPIProvider) FetchCryptoPrices(codes []string) (map[string]float64, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	apiCodes := make([]string, len(codes))
	for i, code := range codes {
		upper := strings.ToUpper(code)
		if mapped, ok := coinAPIAssetMapping[upper]; ok {
			apiCodes[i] = mapped
		} else {
			apiCodes[i] = upper
		}
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(apiCodes, ","))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CoinAPI-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinapi: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string  `json:"asset_id_quote"`
			Rate         float64 `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi parse: %w", err)
	}

	prices := make(map[string]float64, len(result.Rates))
	for _, rate := range result.Rates {
		code := rate.AssetIDQuote
		if reversed, ok := coinAPIReverseMapping[code]; ok {
			code = reversed
		}
		if rate.Rate > 0 {
			prices[code] = rate.Rate
		}
	}
	return prices, nil
}

func (p *CoinAPIProvider) FetchFiatRates(codes []string) (map[string]float64, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("coinapi: api key not configured")
	}

	url := fmt.Sprintf("%s/exchangerate/USD?invert=true&filter_asset_id=%s", p.baseURL, strings.Join(codes, ","))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CoinAPI-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinapi fiat: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Rates []struct {
			AssetIDQuote string  `json:"asset_id_quote"`
			Rate         float64 `json:"rate"`
		} `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("coinapi fiat parse: %w", err)
	}

	rates := make(map[string]float64, len(result.Rates))
	for _, rate := range result.Rates {
		if rate.Rate > 0 {
			rates[rate.AssetIDQuote] = 1.0 / rate.Rate
		}
	}
	return rates, nil
}
```

- [ ] **Step 3: Commit**

```bash
git add app/services/price/coinmarketcap.go app/services/price/coinapi.go
git commit -m "feat: add CoinMarketCap and CoinAPI REST price providers"
```

---

## Task 7: Price Service (Orchestration + Caching)

**Files:**
- Create: `app/services/price/service.go`

- [ ] **Step 1: Create PriceService**

Create `app/services/price/service.go`:

```go
package price

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/redis/go-redis/v9"
)

const redisCurrencyTTL = 60 * time.Second

type Service struct {
	providers    []PriceProvider
	currencyRepo repositories.CurrencyRepository
	redis        *redis.Client
}

func NewService(
	providers []PriceProvider,
	currencyRepo repositories.CurrencyRepository,
	rdb *redis.Client,
) *Service {
	return &Service{
		providers:    providers,
		currencyRepo: currencyRepo,
		redis:        rdb,
	}
}

func (s *Service) RefreshCryptoPrices(ctx context.Context) error {
	cryptos, err := s.currencyRepo.FindActiveCryptos()
	if err != nil {
		return fmt.Errorf("load active cryptos: %w", err)
	}
	if len(cryptos) == 0 {
		return nil
	}

	codes := make([]string, len(cryptos))
	priceMap := make(map[string]float64, len(cryptos))
	for i, c := range cryptos {
		codes[i] = c.Code
		priceMap[c.Code] = c.CurrentPrice
	}

	for _, provider := range s.providers {
		staleCodes := s.findStaleCodes(codes, priceMap, cryptos)
		if len(staleCodes) == 0 {
			break
		}

		prices, err := provider.FetchCryptoPrices(staleCodes)
		if err != nil {
			slog.Warn("crypto price fetch failed", "provider", provider.Name(), "error", err)
			continue
		}

		for code, newPrice := range prices {
			if newPrice <= 0 {
				continue
			}
			oldPrice := priceMap[code]
			if err := s.currencyRepo.UpdatePrice(code, newPrice, oldPrice); err != nil {
				slog.Warn("update crypto price failed", "code", code, "error", err)
				continue
			}
			priceMap[code] = newPrice
			s.cachePrice(ctx, code, newPrice)
			slog.Info("crypto price updated", "provider", provider.Name(), "code", code, "price", newPrice)
		}
	}
	return nil
}

func (s *Service) RefreshFiatRates(ctx context.Context) error {
	fiats, err := s.currencyRepo.FindActiveFiats()
	if err != nil {
		return fmt.Errorf("load active fiats: %w", err)
	}

	codes := make([]string, 0, len(fiats))
	for _, f := range fiats {
		if f.Code == "USD" {
			continue
		}
		codes = append(codes, f.Code)
	}
	if len(codes) == 0 {
		return nil
	}

	for _, provider := range s.providers {
		rates, err := provider.FetchFiatRates(codes)
		if err != nil {
			slog.Warn("fiat rate fetch failed", "provider", provider.Name(), "error", err)
			continue
		}
		if len(rates) == 0 {
			continue
		}

		for code, rate := range rates {
			if rate <= 0 {
				continue
			}
			var oldRate float64
			for _, f := range fiats {
				if f.Code == code {
					oldRate = f.CurrentPrice
					break
				}
			}
			if err := s.currencyRepo.UpdatePrice(code, rate, oldRate); err != nil {
				slog.Warn("update fiat rate failed", "code", code, "error", err)
				continue
			}
			s.cachePrice(ctx, code, rate)
		}
		slog.Info("fiat rates updated", "provider", provider.Name(), "count", len(rates))
		break
	}
	return nil
}

func (s *Service) GetPrice(ctx context.Context, code string) (float64, error) {
	if code == "USD" {
		return 1.0, nil
	}

	if s.redis != nil {
		key := "currency:" + code
		val, err := s.redis.Get(ctx, key).Float64()
		if err == nil && val > 0 {
			return val, nil
		}
	}

	cur, err := s.currencyRepo.FindByCode(code)
	if err != nil {
		return 0, err
	}
	if cur == nil {
		return 0, fmt.Errorf("currency not found: %s", code)
	}
	return cur.CurrentPrice, nil
}

func (s *Service) findStaleCodes(allCodes []string, priceMap map[string]float64, currencies []models.Currency) []string {
	stale := make([]string, 0)
	cutoff := time.Now().Add(-1 * time.Minute)
	for _, c := range currencies {
		if c.PriceUpdatedAt == nil || c.PriceUpdatedAt.Before(cutoff) {
			stale = append(stale, c.Code)
		}
	}
	return stale
}

func (s *Service) cachePrice(ctx context.Context, code string, price float64) {
	if s.redis == nil {
		return
	}
	key := "currency:" + code
	data, _ := json.Marshal(price)
	if err := s.redis.Set(ctx, key, data, redisCurrencyTTL).Err(); err != nil {
		slog.Warn("redis cache currency failed", "code", code, "error", err)
	}
}

func (s *Service) UpdateSinglePrice(ctx context.Context, code string, newPrice float64) error {
	cur, err := s.currencyRepo.FindByCode(code)
	if err != nil || cur == nil {
		return fmt.Errorf("currency not found: %s", code)
	}
	oldPrice := cur.CurrentPrice
	if err := s.currencyRepo.UpdatePrice(code, newPrice, oldPrice); err != nil {
		return err
	}
	s.cachePrice(ctx, code, newPrice)
	return nil
}
```

- [ ] **Step 2: Commit**

```bash
git add app/services/price/service.go
git commit -m "feat: add PriceService with provider fallback chain and Redis caching"
```

---

## Task 8: Conversion Helpers + Tests

**Files:**
- Create: `app/services/price/convert.go`
- Create: `app/services/price/convert_test.go`

- [ ] **Step 1: Create conversion helpers**

Create `app/services/price/convert.go`:

```go
package price

import (
	"context"
	"fmt"
)

func (s *Service) Convert(ctx context.Context, from, to string, amount float64) (float64, error) {
	if from == to {
		return amount, nil
	}
	fromPrice, err := s.GetPrice(ctx, from)
	if err != nil {
		return 0, fmt.Errorf("price for %s: %w", from, err)
	}
	toPrice, err := s.GetPrice(ctx, to)
	if err != nil {
		return 0, fmt.Errorf("price for %s: %w", to, err)
	}
	if toPrice == 0 {
		return 0, fmt.Errorf("zero price for %s", to)
	}
	return amount * fromPrice / toPrice, nil
}

func (s *Service) ConvertToUSD(ctx context.Context, code string, amount float64) (float64, error) {
	return s.Convert(ctx, code, "USD", amount)
}

func (s *Service) ConvertFromUSD(ctx context.Context, code string, amount float64) (float64, error) {
	return s.Convert(ctx, "USD", code, amount)
}

func (s *Service) ConvertCryptoToFiat(ctx context.Context, cryptoCode, fiatCode string, amount float64) (float64, error) {
	return s.Convert(ctx, cryptoCode, fiatCode, amount)
}
```

- [ ] **Step 2: Write tests**

Create `app/services/price/convert_test.go`:

```go
package price

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
)

type mockCurrencyRepo struct {
	currencies map[string]*models.Currency
}

func (m *mockCurrencyRepo) Create(_ *models.Currency) error                           { return nil }
func (m *mockCurrencyRepo) CreateBatch(_ []models.Currency) error                     { return nil }
func (m *mockCurrencyRepo) FindActiveCryptos() ([]models.Currency, error)             { return nil, nil }
func (m *mockCurrencyRepo) FindActiveFiats() ([]models.Currency, error)               { return nil, nil }
func (m *mockCurrencyRepo) FindAllActive() ([]models.Currency, error)                 { return nil, nil }
func (m *mockCurrencyRepo) UpdatePrice(_ string, _, _ float64) error                  { return nil }
func (m *mockCurrencyRepo) UpdatePriceBatch(_ map[string]repositories.PriceUpdate) error { return nil }
func (m *mockCurrencyRepo) FindStale(_ string, _ time.Duration) ([]models.Currency, error) {
	return nil, nil
}

func (m *mockCurrencyRepo) FindByCode(code string) (*models.Currency, error) {
	if c, ok := m.currencies[code]; ok {
		return c, nil
	}
	return nil, nil
}

func newTestService() *Service {
	now := time.Now()
	repo := &mockCurrencyRepo{
		currencies: map[string]*models.Currency{
			"USD":  {ID: uuid.New(), Code: "USD", CurrentPrice: 1.0, PriceUpdatedAt: &now},
			"BTC":  {ID: uuid.New(), Code: "BTC", CurrentPrice: 65000.0, PriceUpdatedAt: &now},
			"ETH":  {ID: uuid.New(), Code: "ETH", CurrentPrice: 3200.0, PriceUpdatedAt: &now},
			"BRL":  {ID: uuid.New(), Code: "BRL", CurrentPrice: 0.196, PriceUpdatedAt: &now},
			"EUR":  {ID: uuid.New(), Code: "EUR", CurrentPrice: 1.09, PriceUpdatedAt: &now},
		},
	}
	return NewService(nil, repo, nil)
}

func TestConvertSameCurrency(t *testing.T) {
	svc := newTestService()
	result, err := svc.Convert(context.Background(), "BTC", "BTC", 1.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 1.5 {
		t.Errorf("expected 1.5, got %f", result)
	}
}

func TestConvertBTCtoUSD(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertToUSD(context.Background(), "BTC", 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 32500.0
	if result != expected {
		t.Errorf("expected %f, got %f", expected, result)
	}
}

func TestConvertBTCtoBRL(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertCryptoToFiat(context.Background(), "BTC", "BRL", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 65000.0 / 0.196
	if abs(result-expected) > 0.01 {
		t.Errorf("expected ~%f, got %f", expected, result)
	}
}

func TestConvertETHtoEUR(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertCryptoToFiat(context.Background(), "ETH", "EUR", 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 2.0 * 3200.0 / 1.09
	if abs(result-expected) > 0.01 {
		t.Errorf("expected ~%f, got %f", expected, result)
	}
}

func TestConvertUnknownCurrency(t *testing.T) {
	svc := newTestService()
	_, err := svc.Convert(context.Background(), "UNKNOWN", "USD", 1.0)
	if err == nil {
		t.Error("expected error for unknown currency")
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
```

Note: The test file needs the `repositories` import for the mock. Add this import to the file:

```go
import (
	"github.com/macrowallets/waas/app/repositories"
)
```

- [ ] **Step 3: Run tests**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/price/... -v`
Expected: All 5 tests pass.

- [ ] **Step 4: Commit**

```bash
git add app/services/price/convert.go app/services/price/convert_test.go
git commit -m "feat: add currency conversion helpers with tests"
```

---

## Task 9: WebSocket Client

**Files:**
- Create: `app/services/price/websocket.go`

- [ ] **Step 1: Create WebSocket client**

Create `app/services/price/websocket.go`:

```go
package price

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/redis/go-redis/v9"
)

const (
	wsURL            = "wss://api-ncsa.coinapi.io/v1/"
	wsUpdateInterval = 10
	wsExchange       = "BINANCE"
)

var wsAssetMapping = map[string]string{
	"MATIC": "POL",
}

var wsReverseMapping map[string]string

func init() {
	wsReverseMapping = make(map[string]string, len(wsAssetMapping))
	for code, asset := range wsAssetMapping {
		wsReverseMapping[asset] = code
	}
}

type WebSocketClient struct {
	apiKey       string
	currencyRepo repositories.CurrencyRepository
	redis        *redis.Client
	activeCodes  []string
}

func NewWebSocketClient(apiKey string, currencyRepo repositories.CurrencyRepository, rdb *redis.Client) *WebSocketClient {
	return &WebSocketClient{
		apiKey:       apiKey,
		currencyRepo: currencyRepo,
		redis:        rdb,
	}
}

func (w *WebSocketClient) Connect(ctx context.Context) error {
	if err := w.refreshActiveCodes(); err != nil {
		return fmt.Errorf("load active codes: %w", err)
	}
	if len(w.activeCodes) == 0 {
		return fmt.Errorf("no active crypto currencies to track")
	}

	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		slog.Info("connecting to CoinAPI WebSocket", "url", wsURL)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
		if err != nil {
			slog.Error("websocket dial failed", "error", err)
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		backoff = time.Second

		hello := w.buildHelloMessage()
		if err := conn.WriteJSON(hello); err != nil {
			slog.Error("websocket send hello failed", "error", err)
			conn.Close()
			continue
		}
		slog.Info("CoinAPI WebSocket connected", "codes", len(w.activeCodes))

		refreshTicker := time.NewTicker(60 * time.Second)
		done := make(chan struct{})

		go func() {
			defer close(done)
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					slog.Error("websocket read failed", "error", err)
					return
				}
				w.processMessage(ctx, message)
			}
		}()

		select {
		case <-done:
			refreshTicker.Stop()
			conn.Close()
			slog.Warn("websocket disconnected, reconnecting...")
		case <-ctx.Done():
			refreshTicker.Stop()
			conn.Close()
			return ctx.Err()
		case <-refreshTicker.C:
			_ = w.refreshActiveCodes()
		}
	}
}

func (w *WebSocketClient) buildHelloMessage() map[string]interface{} {
	assets := make([]string, len(w.activeCodes))
	for i, code := range w.activeCodes {
		asset := code
		if mapped, ok := wsAssetMapping[code]; ok {
			asset = mapped
		}
		assets[i] = asset + "/USD"
	}

	return map[string]interface{}{
		"type":                          "hello",
		"apikey":                        w.apiKey,
		"heartbeat":                     false,
		"subscribe_data_type":           []string{"exrate"},
		"subscribe_filter_asset_id":     assets,
		"subscribe_filter_exchange_id":  []string{wsExchange},
		"subscribe_update_limit_ms_exrate": wsUpdateInterval * 1000,
	}
}

func (w *WebSocketClient) processMessage(ctx context.Context, data []byte) {
	var msg struct {
		AssetIDBase string  `json:"asset_id_base"`
		Rate        float64 `json:"rate"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	if msg.Rate <= 0 {
		return
	}

	code := msg.AssetIDBase
	if reversed, ok := wsReverseMapping[code]; ok {
		code = reversed
	}

	found := false
	for _, c := range w.activeCodes {
		if strings.EqualFold(c, code) {
			found = true
			break
		}
	}
	if !found {
		return
	}

	cur, err := w.currencyRepo.FindByCode(code)
	if err != nil || cur == nil {
		return
	}

	oldPrice := cur.CurrentPrice
	if err := w.currencyRepo.UpdatePrice(code, msg.Rate, oldPrice); err != nil {
		slog.Warn("ws update price failed", "code", code, "error", err)
		return
	}

	if w.redis != nil {
		key := "currency:" + code
		priceJSON, _ := json.Marshal(msg.Rate)
		w.redis.Set(ctx, key, priceJSON, redisCurrencyTTL)
	}

	slog.Info("ws price updated", "code", code, "price", msg.Rate)
}

func (w *WebSocketClient) refreshActiveCodes() error {
	cryptos, err := w.currencyRepo.FindActiveCryptos()
	if err != nil {
		return err
	}
	codes := make([]string, len(cryptos))
	for i, c := range cryptos {
		codes[i] = c.Code
	}
	w.activeCodes = codes
	return nil
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Add gorilla/websocket dependency**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go get github.com/gorilla/websocket`

- [ ] **Step 3: Commit**

```bash
git add app/services/price/websocket.go go.mod go.sum
git commit -m "feat: add CoinAPI WebSocket client for real-time crypto prices"
```

---

## Task 10: Artisan Commands (price:websocket + price:check-update)

**Files:**
- Create: `app/console/commands/price_websocket.go`
- Create: `app/console/commands/price_check_update.go`
- Modify: `bootstrap/app.go`

- [ ] **Step 1: Create price:websocket command**

Create `app/console/commands/price_websocket.go`:

```go
package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/price"
)

type PriceWebSocket struct{}

func (c *PriceWebSocket) Signature() string {
	return "price:websocket"
}

func (c *PriceWebSocket) Description() string {
	return "Connect to CoinAPI WebSocket for real-time crypto price updates"
}

func (c *PriceWebSocket) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceWebSocket) Handle(ctx console.Context) error {
	ctr := container.Get()

	ctx.Info("refreshing initial prices...")
	bgCtx := context.Background()
	if err := ctr.PriceService.RefreshCryptoPrices(bgCtx); err != nil {
		ctx.Error("initial crypto refresh failed: " + err.Error())
	}
	if err := ctr.PriceService.RefreshFiatRates(bgCtx); err != nil {
		ctx.Error("initial fiat refresh failed: " + err.Error())
	}
	ctx.Info("initial prices refreshed")

	apiKey := ctr.PriceConfig.CoinAPIKey
	if apiKey == "" {
		ctx.Error("COINAPI_API_KEY is not configured")
		return nil
	}

	ws := price.NewWebSocketClient(apiKey, ctr.CurrencyRepo, ctr.Redis)
	ctx.Info("starting CoinAPI WebSocket connection...")
	return ws.Connect(bgCtx)
}
```

- [ ] **Step 2: Create price:check-update command**

Create `app/console/commands/price_check_update.go`:

```go
package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
)

type PriceCheckUpdate struct{}

func (c *PriceCheckUpdate) Signature() string {
	return "price:check-update"
}

func (c *PriceCheckUpdate) Description() string {
	return "Check for stale currency prices and trigger REST refresh"
}

func (c *PriceCheckUpdate) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceCheckUpdate) Handle(ctx console.Context) error {
	ctr := container.Get()
	bgCtx := context.Background()

	staleCryptos, err := ctr.CurrencyRepo.FindStale(models.CurrencyTypeCrypto, 1*time.Minute)
	if err != nil {
		ctx.Error("failed to check stale cryptos: " + err.Error())
		return err
	}

	if len(staleCryptos) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale crypto currencies, refreshing...", len(staleCryptos)))
		if err := ctr.PriceService.RefreshCryptoPrices(bgCtx); err != nil {
			ctx.Error("crypto refresh failed: " + err.Error())
		}

		stillStale, _ := ctr.CurrencyRepo.FindStale(models.CurrencyTypeCrypto, 1*time.Minute)
		if len(stillStale) > 0 {
			codes := make([]string, len(stillStale))
			for i, c := range stillStale {
				codes[i] = c.Code
			}
			ctx.Error(fmt.Sprintf("still stale after all providers: %v", codes))
		} else {
			ctx.Info("all crypto prices refreshed successfully")
		}
	} else {
		ctx.Info("all crypto prices are up to date")
	}

	staleFiats, err := ctr.CurrencyRepo.FindStale(models.CurrencyTypeFiat, 1*time.Hour)
	if err != nil {
		ctx.Error("failed to check stale fiats: " + err.Error())
		return err
	}

	if len(staleFiats) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale fiat currencies, refreshing...", len(staleFiats)))
		if err := ctr.PriceService.RefreshFiatRates(bgCtx); err != nil {
			ctx.Error("fiat refresh failed: " + err.Error())
		}
	} else {
		ctx.Info("all fiat rates are up to date")
	}

	return nil
}
```

- [ ] **Step 3: Register commands in bootstrap/app.go**

Add to the `WithCommands` slice in `bootstrap/app.go`:

```go
&commands.PriceWebSocket{},
&commands.PriceCheckUpdate{},
```

- [ ] **Step 4: Commit**

```bash
git add app/console/commands/price_websocket.go app/console/commands/price_check_update.go bootstrap/app.go
git commit -m "feat: add price:websocket and price:check-update artisan commands"
```

---

## Task 11: Container Wiring + Config

**Files:**
- Modify: `app/container/container.go`
- Modify: `app/providers/vault_container.go`
- Modify: `config/vault.go`
- Modify: `main.go`
- Modify: `.env.dev.example`

- [ ] **Step 1: Add fields to Container**

In `app/container/container.go`, add these fields to the `Container` struct:

```go
CurrencyRepo repositories.CurrencyRepository
PriceService *price.Service
PriceConfig  PriceConfig
```

Add the PriceConfig struct:

```go
type PriceConfig struct {
	CoinGeckoAPIKey      string
	CoinMarketCapAPIKey  string
	CoinAPIKey           string
}
```

Add the import for `price`:

```go
"github.com/macrowallets/waas/app/services/price"
```

- [ ] **Step 2: Wire in vault_container.go**

In `app/providers/vault_container.go`, in `buildVaultContainer()`, after `c.WalletSyncStateRepo = ...` and before `providerMap := make(...)`, add:

```go
c.CurrencyRepo = repositories.NewCurrencyRepository()

c.PriceConfig = container.PriceConfig{
    CoinGeckoAPIKey:     facades.Config().GetString("vault.price.coingecko_api_key"),
    CoinMarketCapAPIKey: facades.Config().GetString("vault.price.coinmarketcap_api_key"),
    CoinAPIKey:          facades.Config().GetString("vault.price.coinapi_api_key"),
}

priceProviders := []price.PriceProvider{
    price.NewCoinGeckoProvider(c.PriceConfig.CoinGeckoAPIKey),
}
if c.PriceConfig.CoinMarketCapAPIKey != "" {
    priceProviders = append(priceProviders, price.NewCoinMarketCapProvider(c.PriceConfig.CoinMarketCapAPIKey))
}
if c.PriceConfig.CoinAPIKey != "" {
    priceProviders = append(priceProviders, price.NewCoinAPIProvider(c.PriceConfig.CoinAPIKey))
}
c.PriceService = price.NewService(priceProviders, c.CurrencyRepo, c.Redis)
```

Add import for `price`:

```go
"github.com/macrowallets/waas/app/services/price"
```

- [ ] **Step 3: Add config keys**

In `config/vault.go`, add inside the `vault` map:

```go
"price": map[string]any{
    "coingecko_api_key":      envString("COINGECKO_API_KEY", ""),
    "coinmarketcap_api_key":  envString("COINMARKETCAP_API_KEY", ""),
    "coinapi_api_key":        envString("COINAPI_API_KEY", ""),
},
```

- [ ] **Step 4: Add price_updater Lambda mode**

In `main.go`, add to the switch:

```go
case "price_updater":
    lambda.Start(handlePriceUpdate)
```

Add the handler function:

```go
func handlePriceUpdate(ctx context.Context) error {
    slog.Info("price update triggered")
    if err := c.PriceService.RefreshCryptoPrices(ctx); err != nil {
        slog.Error("crypto price refresh failed", "error", err)
    }
    if err := c.PriceService.RefreshFiatRates(ctx); err != nil {
        slog.Error("fiat rate refresh failed", "error", err)
    }
    return nil
}
```

- [ ] **Step 5: Update .env.dev.example**

Add to `.env.dev.example`:

```
# Price Provider API Keys
COINGECKO_API_KEY=
COINMARKETCAP_API_KEY=
COINAPI_API_KEY=
```

- [ ] **Step 6: Verify build**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Build succeeds.

- [ ] **Step 7: Commit**

```bash
git add app/container/container.go app/providers/vault_container.go config/vault.go main.go .env.dev.example
git commit -m "feat: wire currency system into DI container, config, and Lambda"
```

---

## Task 12: API Endpoints (Currency + Preferences + Convert)

**Files:**
- Create: `app/http/controllers/currency_controller.go`
- Create: `app/http/controllers/preferences_controller.go`
- Create: `app/http/requests/update_preferences_request.go`
- Modify: `routes/admin.go`

- [ ] **Step 1: Create CurrencyController**

Create `app/http/controllers/currency_controller.go`:

```go
package controllers

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

func ListCurrencies(ctx http.Context) http.Response {
	currencies, err := container.Get().CurrencyRepo.FindAllActive()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch currencies"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": currencies})
}

func GetCurrency(ctx http.Context) http.Response {
	code := ctx.Request().Route("code")
	if code == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "currency code is required"})
	}

	currency, err := container.Get().CurrencyRepo.FindByCode(code)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch currency"})
	}
	if currency == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "currency not found"})
	}
	return ctx.Response().Json(http.StatusOK, currency)
}

func ConvertCurrency(ctx http.Context) http.Response {
	from := ctx.Request().Query("from", "")
	to := ctx.Request().Query("to", "")
	amountStr := ctx.Request().Query("amount", "0")

	if from == "" || to == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "from, to, and amount are required"})
	}

	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "amount must be a positive number"})
	}

	result, err := container.Get().PriceService.Convert(ctx.Request().Origin().Context(), from, to, amount)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	rate := result / amount
	return ctx.Response().Json(http.StatusOK, http.Json{
		"from":   from,
		"to":     to,
		"amount": amount,
		"result": result,
		"rate":   rate,
	})
}
```

- [ ] **Step 2: Create PreferencesController**

Create `app/http/controllers/preferences_controller.go`:

```go
package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

func GetPreferences(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)
	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":    prefs.IsDisplayInFiat(),
	})
}

func UpdatePreferences(ctx http.Context) http.Response {
	userID := ctx.Value("user_id").(uuid.UUID)
	user := ctx.Value("user").(*models.User)

	var req requests.UpdatePreferencesRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	prefs := user.Preferences
	if prefs == nil {
		prefs = &models.UserPreferences{}
	}

	if req.PreferredFiatCode != "" {
		cur, err := container.Get().CurrencyRepo.FindByCode(req.PreferredFiatCode)
		if err != nil || cur == nil || !cur.Active || cur.Type != models.CurrencyTypeFiat {
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid fiat currency code"})
		}
		prefs.PreferredFiatCode = req.PreferredFiatCode
	}
	if req.DisplayInFiat != nil {
		prefs.DisplayInFiat = req.DisplayInFiat
	}

	if err := container.Get().UserRepo.UpdatePreferences(userID, prefs); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update preferences"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"preferred_fiat_code": prefs.GetPreferredFiat(),
		"display_in_fiat":    prefs.IsDisplayInFiat(),
	})
}
```

- [ ] **Step 3: Create validation request**

Create `app/http/requests/update_preferences_request.go`:

```go
package requests

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"
)

type UpdatePreferencesRequest struct {
	PreferredFiatCode string `form:"preferred_fiat_code" json:"preferred_fiat_code"`
	DisplayInFiat     *bool  `form:"display_in_fiat" json:"display_in_fiat"`
}

func (r *UpdatePreferencesRequest) Authorize(_ http.Context) error {
	return nil
}

func (r *UpdatePreferencesRequest) Rules(_ http.Context) map[string]string {
	return map[string]string{
		"preferred_fiat_code": "max_len:20",
	}
}

func (r *UpdatePreferencesRequest) Messages(_ http.Context) map[string]string {
	return map[string]string{}
}

func (r *UpdatePreferencesRequest) Attributes(_ http.Context) map[string]string {
	return map[string]string{}
}

func (r *UpdatePreferencesRequest) PrepareForValidation(_ http.Context, data validation.Data) error {
	return nil
}
```

- [ ] **Step 4: Add routes**

In `routes/admin.go`, add after the existing `/v1/users` group:

```go
facades.Route().Prefix("/v1/currencies").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
    router.Get("", controllers.ListCurrencies)
    router.Get("/{code}", controllers.GetCurrency)
})

facades.Route().Prefix("/v1/me").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
    router.Get("/preferences", controllers.GetPreferences)
    router.Put("/preferences", controllers.UpdatePreferences)
})

facades.Route().Prefix("/v1/convert").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
    router.Get("", controllers.ConvertCurrency)
})
```

- [ ] **Step 5: Verify build**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add app/http/controllers/currency_controller.go app/http/controllers/preferences_controller.go app/http/requests/update_preferences_request.go routes/admin.go
git commit -m "feat: add currency, preferences, and convert API endpoints"
```

---

## Task 13: Frontend Types + Hooks

**Files:**
- Create: `front/src/types/currency.ts`
- Create: `front/src/hooks/useCurrencies.ts`
- Create: `front/src/hooks/usePreferences.ts`
- Modify: `front/src/types/user.ts`

- [ ] **Step 1: Create currency types**

Create `front/src/types/currency.ts`:

```typescript
export interface Currency {
  id: string;
  name: string;
  code: string;
  symbol: string;
  type: "crypto" | "fiat";
  logo?: string;
  subunits: number;
  current_price: number;
  active: boolean;
  price_updated_at?: string;
}

export interface UserPreferences {
  preferred_fiat_code: string;
  display_in_fiat: boolean;
}

export interface ConvertResponse {
  from: string;
  to: string;
  amount: number;
  result: number;
  rate: number;
}
```

- [ ] **Step 2: Add preferences to User type**

In `front/src/types/user.ts`, add to the `User` interface:

```typescript
preferences?: {
  preferred_fiat_code: string;
  display_in_fiat: boolean;
};
```

- [ ] **Step 3: Create useCurrencies hook**

Create `front/src/hooks/useCurrencies.ts`:

```typescript
import useSWR from "swr";
import { api } from "@/lib/api";
import type { Currency } from "@/types/currency";

const fetcher = (url: string) => api.get(url).then((r) => r.data);

export function useCurrencies() {
  const { data, error, isLoading, mutate } = useSWR<{ data: Currency[] }>(
    "/v1/currencies",
    fetcher,
    { refreshInterval: 30000 }
  );

  const currencies = data?.data ?? [];
  const cryptos = currencies.filter((c) => c.type === "crypto");
  const fiats = currencies.filter((c) => c.type === "fiat");

  const cryptoToUsd: Record<string, number> = {};
  for (const c of cryptos) {
    cryptoToUsd[c.code] = c.current_price;
  }

  const usdToFiat: Record<string, number> = {};
  const fiatSymbols: Record<string, string> = {};
  for (const f of fiats) {
    if (f.current_price > 0) {
      usdToFiat[f.code] = 1 / f.current_price;
    }
    fiatSymbols[f.code] = f.symbol;
  }
  usdToFiat["USD"] = 1;
  fiatSymbols["USD"] = "$";

  function convert(amount: number, from: string, to: string): number {
    if (from === to) return amount;
    const fromPrice = cryptoToUsd[from] ?? (from === "USD" ? 1 : 1 / (usdToFiat[from] ?? 1));
    const toPrice = cryptoToUsd[to] ?? (to === "USD" ? 1 : 1 / (usdToFiat[to] ?? 1));
    if (toPrice === 0) return 0;
    return (amount * fromPrice) / toPrice;
  }

  function convertCryptoToFiat(amount: number, cryptoCode: string, fiatCode: string): number {
    const usdValue = amount * (cryptoToUsd[cryptoCode] ?? 0);
    return usdValue * (usdToFiat[fiatCode] ?? 1);
  }

  function formatFiat(amount: number, fiatCode: string): string {
    const symbol = fiatSymbols[fiatCode] ?? fiatCode;
    const fiat = fiats.find((f) => f.code === fiatCode);
    const decimals = fiat?.subunits ?? 2;
    const formatted = amount.toLocaleString(undefined, {
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    });
    return `${symbol}${formatted}`;
  }

  return {
    currencies,
    cryptos,
    fiats,
    cryptoToUsd,
    usdToFiat,
    fiatSymbols,
    convert,
    convertCryptoToFiat,
    formatFiat,
    isLoading,
    error,
    mutate,
  };
}
```

- [ ] **Step 4: Create usePreferences hook**

Create `front/src/hooks/usePreferences.ts`:

```typescript
import useSWR from "swr";
import { api } from "@/lib/api";
import type { UserPreferences } from "@/types/currency";

const fetcher = (url: string) => api.get(url).then((r) => r.data);

export function usePreferences() {
  const { data, error, isLoading, mutate } = useSWR<UserPreferences>(
    "/v1/me/preferences",
    fetcher
  );

  const preferences: UserPreferences = {
    preferred_fiat_code: data?.preferred_fiat_code ?? "USD",
    display_in_fiat: data?.display_in_fiat ?? true,
  };

  async function updatePreferences(patch: Partial<UserPreferences>) {
    await api.put("/v1/me/preferences", patch);
    mutate();
  }

  return {
    preferences,
    activeFiatCode: preferences.preferred_fiat_code,
    displayInFiat: preferences.display_in_fiat,
    updatePreferences,
    isLoading,
    error,
  };
}
```

- [ ] **Step 5: Commit**

```bash
cd /home/raphaelcangucu/macro-wallets/front
git add src/types/currency.ts src/types/user.ts src/hooks/useCurrencies.ts src/hooks/usePreferences.ts
git commit -m "feat: add currency types and useCurrencies/usePreferences hooks"
```

---

## Task 14: Frontend — Currency Preference Settings Tab

**Files:**
- Create: `front/src/components/Settings/CurrencyPreferenceTab.tsx`
- Modify: `front/src/pages/dashboard/settings/index.tsx`

- [ ] **Step 1: Create CurrencyPreferenceTab component**

Create `front/src/components/Settings/CurrencyPreferenceTab.tsx`:

```tsx
import { useState } from "react";
import { Button, Switch, RadioGroup, Radio } from "@heroui/react";
import { useCurrencies } from "@/hooks/useCurrencies";
import { usePreferences } from "@/hooks/usePreferences";

export function CurrencyPreferenceTab() {
  const { fiats } = useCurrencies();
  const { preferences, updatePreferences } = usePreferences();
  const [selectedFiat, setSelectedFiat] = useState(preferences.preferred_fiat_code);
  const [displayInFiat, setDisplayInFiat] = useState(preferences.display_in_fiat);
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    setSaving(true);
    try {
      await updatePreferences({
        preferred_fiat_code: selectedFiat,
        display_in_fiat: displayInFiat,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h3 className="text-lg font-semibold mb-2">Display Currency</h3>
        <p className="text-sm text-default-500 mb-4">
          Choose your preferred fiat currency for displaying wallet balances and totals.
        </p>
      </div>

      <div className="flex items-center gap-3">
        <Switch isSelected={displayInFiat} onValueChange={setDisplayInFiat} />
        <div>
          <p className="text-sm font-medium">Display balances in fiat</p>
          <p className="text-xs text-default-400">
            Show crypto balances converted to your preferred fiat currency
          </p>
        </div>
      </div>

      <RadioGroup
        label="Preferred Fiat Currency"
        value={selectedFiat}
        onValueChange={setSelectedFiat}
        classNames={{ wrapper: "grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 gap-3" }}
      >
        {fiats.map((fiat) => (
          <Radio key={fiat.code} value={fiat.code} classNames={{ label: "text-sm" }}>
            {fiat.symbol} {fiat.code}
          </Radio>
        ))}
      </RadioGroup>

      <Button color="primary" isLoading={saving} onPress={handleSave}>
        Save Preferences
      </Button>
    </div>
  );
}
```

- [ ] **Step 2: Add tab to settings page**

In `front/src/pages/dashboard/settings/index.tsx`, add the `CurrencyPreferenceTab` as a new tab (import and add to the tab list alongside `general`, `developer`, `users`):

Import:
```typescript
import { CurrencyPreferenceTab } from "@/components/Settings/CurrencyPreferenceTab";
```

Add a new tab `currency` with label "Currency" rendering `<CurrencyPreferenceTab />`.

- [ ] **Step 3: Commit**

```bash
cd /home/raphaelcangucu/macro-wallets/front
git add src/components/Settings/CurrencyPreferenceTab.tsx src/pages/dashboard/settings/index.tsx
git commit -m "feat: add currency preference settings tab"
```

---

## Task 15: Frontend — Update Asset Display to Use Preferred Fiat

**Files:**
- Modify: `front/src/components/Assets/AssetsByWallets.tsx`
- Modify: `front/src/components/Assets/AssetsByAssets.tsx`

- [ ] **Step 1: Update AssetsByWallets**

In `front/src/components/Assets/AssetsByWallets.tsx`, add the hooks:

```typescript
import { useCurrencies } from "@/hooks/useCurrencies";
import { usePreferences } from "@/hooks/usePreferences";
```

Inside the component, add:

```typescript
const { convertCryptoToFiat, formatFiat } = useCurrencies();
const { activeFiatCode } = usePreferences();
```

Replace any hardcoded `$` + `balance_usd` display with:

```typescript
const fiatValue = wallet.balance && wallet.balance_asset
  ? convertCryptoToFiat(parseFloat(wallet.balance), wallet.balance_asset.toUpperCase(), activeFiatCode)
  : wallet.balance_usd ?? 0;
const displayValue = formatFiat(fiatValue, activeFiatCode);
```

Use `displayValue` where `balance_usd` was previously shown.

- [ ] **Step 2: Update AssetsByAssets**

Apply the same pattern in `front/src/components/Assets/AssetsByAssets.tsx`:

```typescript
import { useCurrencies } from "@/hooks/useCurrencies";
import { usePreferences } from "@/hooks/usePreferences";
```

Replace portfolio totals and per-chain USD calculations with `convertCryptoToFiat` + `formatFiat` using `activeFiatCode`.

- [ ] **Step 3: Verify frontend builds**

Run: `cd /home/raphaelcangucu/macro-wallets/front && npm run typecheck`
Expected: No type errors.

- [ ] **Step 4: Commit**

```bash
cd /home/raphaelcangucu/macro-wallets/front
git add src/components/Assets/AssetsByWallets.tsx src/components/Assets/AssetsByAssets.tsx
git commit -m "feat: display wallet balances in user's preferred fiat currency"
```

---

## Self-Review

### Spec Coverage

| Spec Requirement | Task |
|-----------------|------|
| `currencies` table (crypto + fiat) | Task 1 |
| User preferences JSONB on `users` | Task 2 |
| CurrencyRepository | Task 3 |
| Seed ~180 fiats + ~20 cryptos | Task 4 |
| PriceProvider interface | Task 5 |
| CoinGecko provider | Task 5 |
| CoinMarketCap provider | Task 6 |
| CoinAPI REST provider | Task 6 |
| PriceService orchestration + caching | Task 7 |
| Conversion helpers + tests | Task 8 |
| CoinAPI WebSocket client | Task 9 |
| `price:websocket` command | Task 10 |
| `price:check-update` failover command | Task 10 |
| Container wiring + config | Task 11 |
| `price_updater` Lambda mode | Task 11 |
| Currency API endpoints | Task 12 |
| Preferences API endpoints | Task 12 |
| Convert API endpoint | Task 12 |
| Frontend types | Task 13 |
| useCurrencies hook | Task 13 |
| usePreferences hook | Task 13 |
| Fiat selector UI | Task 14 |
| Asset display in preferred fiat | Task 15 |

### Placeholder Scan

No TBD, TODO, or "implement later" items found. All steps have concrete code.

### Type Consistency

- `PriceProvider` interface used consistently across Tasks 5, 6, 7, 11.
- `CurrencyRepository` interface defined in Task 3, referenced in Tasks 7, 9, 10, 11, 12.
- `UserPreferences` struct defined in Task 2, used in Tasks 3, 12, 13.
- `PriceUpdate` struct defined in Task 3, used in mock in Task 8.
- `PriceConfig` struct defined in Task 11, used in Task 10.
