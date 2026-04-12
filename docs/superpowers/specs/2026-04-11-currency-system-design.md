# Currency System — Design Spec

**Date:** 2026-04-11
**Status:** Draft
**Ported from:** gamba/backend (Laravel/PHP) → macro-wallets (Goravel/Go)

## Overview

Add a `currencies` table, real-time price tracking, provider-agnostic price fetching, user fiat preferences, and frontend display of wallet values in the user's preferred fiat currency.

## Goals

1. Track crypto-to-USD and fiat-to-USD exchange rates in a single `currencies` table
2. Real-time crypto price updates via CoinAPI WebSocket
3. REST fallback chain: CoinGecko (primary) → CoinMarketCap (secondary)
4. Scheduled failover command that detects stale prices and triggers REST refresh
5. User preference for preferred fiat currency (default USD) stored as JSONB on `users`
6. Frontend displays wallet balances and portfolio totals in the user's chosen fiat
7. Conversion helpers: `Convert(from, to, amount)`, `ConvertCryptoToFiat(crypto, fiat, amount)`

## Non-Goals

- User-facing currency swap/exchange feature (no balance transfers between currencies)
- Order books or trading functionality
- Historical price charts (future enhancement)

---

## 1. Database Schema

### 1.1 `currencies` table (new)

```sql
CREATE TYPE currency_type AS ENUM ('crypto', 'fiat');

CREATE TABLE currencies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(100) NOT NULL,
    code          VARCHAR(20)  NOT NULL UNIQUE,
    symbol        VARCHAR(10),
    type          currency_type NOT NULL,
    logo          VARCHAR(500),
    subunits      INT          NOT NULL DEFAULT 2,
    current_price DECIMAL(28,10) NOT NULL DEFAULT 1,
    last_price    DECIMAL(28,10),
    price_updated_at TIMESTAMPTZ,
    active        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_currencies_type_active ON currencies (type, active);
CREATE INDEX idx_currencies_code ON currencies (code);
```

**Price semantics:**
- USD row: `current_price = 1` (always, never updated)
- Crypto rows: `current_price` = how many USD per 1 unit (e.g. BTC = 65000.00)
- Fiat rows: `current_price` = how many USD per 1 unit of that fiat (e.g. EUR = 1.09, BRL = 0.196)

**Conversion formula:** `amount_in_target = amount * price_of(from) / price_of(to)`

### 1.2 `users` table — add `preferences` column

```sql
ALTER TABLE users ADD COLUMN preferences JSONB NOT NULL DEFAULT '{}';
```

JSONB structure:

```json
{
  "preferred_fiat_code": "USD",
  "display_in_fiat": true
}
```

Code defaults: if `preferred_fiat_code` is empty → `"USD"`. If `display_in_fiat` is nil → `true`.

---

## 2. Backend — Models

### 2.1 Currency model — `app/models/currency.go`

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
```

### 2.2 User preferences — `app/models/user_preferences.go`

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

Add to `User` struct:

```go
Preferences *UserPreferences `gorm:"type:jsonb;serializer:json" json:"preferences,omitempty"`
```

---

## 3. Backend — Repository

### 3.1 `CurrencyRepository` — `app/repositories/currency_repository.go`

```go
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

type PriceUpdate struct {
    CurrentPrice float64
    LastPrice    float64
}
```

All methods use `facades.Orm().Query()` following existing repo patterns.

### 3.2 `UserRepository` — extend existing

Add to existing interface:

```go
UpdatePreferences(id uuid.UUID, prefs *models.UserPreferences) error
```

Implementation merges into the JSONB column.

---

## 4. Backend — Price Service (Provider-Agnostic)

### 4.1 Provider interface — `app/services/price/provider.go`

```go
package price

type PriceProvider interface {
    Name() string
    FetchCryptoPrices(codes []string) (map[string]float64, error)
    FetchFiatRates(baseCodes []string) (map[string]float64, error)
}
```

Returns `map[code]priceInUSD`. Implementations handle API-specific quirks (rate limits, auth, code mapping like MATIC→POL).

### 4.2 Implementations

| File | Provider | Role | Auth |
|------|----------|------|------|
| `coingecko.go` | CoinGecko `/simple/price` | Primary REST | API key (free tier) |
| `coinmarketcap.go` | CoinMarketCap `/v1/cryptocurrency/quotes/latest` | Secondary REST | API key |
| `coinapi.go` | CoinAPI `/v1/exchangerate/{base}` | Tertiary REST | API key |

Each implementation:
- Maps internal codes to provider-specific IDs (e.g. MATIC→POL for CoinAPI)
- Returns `map[string]float64` keyed by our internal code
- Handles errors gracefully (returns partial results if some codes fail)

### 4.3 Price service — `app/services/price/service.go`

```go
type Service struct {
    providers    []PriceProvider // ordered: CoinGecko, CoinMarketCap, CoinAPI
    currencyRepo repositories.CurrencyRepository
    redis        *redis.Client
}

func NewService(
    providers []PriceProvider,
    currencyRepo repositories.CurrencyRepository,
    redis *redis.Client,
) *Service

// Tries providers in order until all stale codes are refreshed
func (s *Service) RefreshCryptoPrices(ctx context.Context) error
func (s *Service) RefreshFiatRates(ctx context.Context) error

// Redis-first, DB fallback
func (s *Service) GetPrice(code string) (float64, error)

// Conversion helpers
func (s *Service) Convert(from, to string, amount float64) (float64, error)
func (s *Service) ConvertToUSD(code string, amount float64) (float64, error)
func (s *Service) ConvertFromUSD(code string, amount float64) (float64, error)
func (s *Service) ConvertCryptoToFiat(cryptoCode, fiatCode string, amount float64) (float64, error)
```

**Redis caching:** Each price update writes `currency:{CODE}` with 60s TTL (JSON of the currency row). `GetPrice` reads Redis first, falls back to DB.

**RefreshCryptoPrices flow:**
1. Load active cryptos from DB
2. For each provider (in priority order):
   a. Fetch prices for all codes that are still stale
   b. Update DB + Redis for successfully fetched codes
   c. If all codes are fresh, stop
3. Return error only if all providers fail for some codes

### 4.4 WebSocket client — `app/services/price/websocket.go`

Extracted from the command for testability:

```go
type WebSocketClient struct {
    apiKey       string
    service      *Service
    currencyRepo repositories.CurrencyRepository
    redis        *redis.Client
}

func (w *WebSocketClient) Connect(ctx context.Context) error
func (w *WebSocketClient) reconnectWithBackoff(ctx context.Context) error
```

Uses `gorilla/websocket`. Connects to `wss://api-ncsa.coinapi.io/v1/`, subscribes to `exrate` for active cryptos via BINANCE exchange, 10s update interval.

On each message:
1. Parse `asset_id_base` + `rate`
2. Map asset to internal code (handle MATIC→POL etc.)
3. Update DB: `current_price = rate`, `last_price = old current_price`, `price_updated_at = now`
4. Write to Redis: `currency:{CODE}` with 60s TTL
5. Log: `[BTC][2026-04-11 14:30:00]: 65432.10`

---

## 5. Backend — Commands

### 5.1 `price:websocket` — long-running

```
Signature: price:websocket
Description: Connect to CoinAPI WebSocket for real-time crypto prices
```

1. On start: call `PriceService.RefreshCryptoPrices()` + `RefreshFiatRates()` for initial values
2. Start WebSocket connection
3. Periodic timer (60s): refresh active currency list from DB
4. On disconnect: reconnect with exponential backoff (1s → 2s → 4s → ... → 60s max)

Runs as a supervised process (systemd, supervisor, or Docker) in production. Not a Lambda mode — it's long-lived.

### 5.2 `price:check-update` — failover (scheduled)

```
Signature: price:check-update
Description: Check for stale currency prices and trigger REST refresh
```

Flow:
1. Query cryptos where `price_updated_at < now() - 1 minute`
2. If stale: call `PriceService.RefreshCryptoPrices()` (tries providers in order)
3. Re-query — if still stale after all providers, log warning
4. Query fiats where `price_updated_at < now() - 1 hour`
5. If stale: call `PriceService.RefreshFiatRates()`
6. Re-query — if still stale, log warning

**Production:** Add `price_updater` Lambda mode to `main.go` switch, triggered by EventBridge every 1 minute.
**Local dev:** Run manually via `go run . price:check-update` or set up a cron/watch loop.

---

## 6. Backend — API Endpoints

### 6.1 Currencies

```
GET /v1/currencies              → CurrencyController.List (session auth)
GET /v1/currencies/:code        → CurrencyController.Show (session auth)
GET /api/v1/currencies          → CurrencyController.List (API token auth)
GET /api/v1/currencies/:code    → CurrencyController.Show (API token auth)
```

Response for `GET /v1/currencies`:

```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Bitcoin",
      "code": "BTC",
      "symbol": "₿",
      "type": "crypto",
      "subunits": 8,
      "current_price": 65432.10,
      "active": true,
      "price_updated_at": "2026-04-11T14:30:00Z"
    },
    {
      "id": "uuid",
      "name": "Brazilian Real",
      "code": "BRL",
      "symbol": "R$",
      "type": "fiat",
      "subunits": 2,
      "current_price": 0.196,
      "active": true,
      "price_updated_at": "2026-04-11T14:00:00Z"
    }
  ]
}
```

### 6.2 User Preferences

```
GET /v1/me/preferences          → PreferencesController.Show
PUT /v1/me/preferences          → PreferencesController.Update
```

Request body for `PUT`:

```json
{
  "preferred_fiat_code": "BRL",
  "display_in_fiat": true
}
```

Validation: `preferred_fiat_code` must be a valid active fiat currency code.

### 6.3 Convert (utility)

```
GET /v1/convert?from=BTC&to=BRL&amount=0.5 → CurrencyController.Convert
```

Response:

```json
{
  "from": "BTC",
  "to": "BRL",
  "amount": 0.5,
  "result": 166540.56,
  "rate": 333081.12
}
```

---

## 7. Backend — Seeds

### 7.1 Crypto currencies (~20 active)

Seeded from supported chains/tokens. Active: BTC, ETH, SOL, MATIC, LTC, DOGE, USDT, USDC, XRP, BNB, TRX, ADA, DOT, LINK, AVAX, BCH, DAI, TON.

Initial `current_price` set to 0 — populated on first `price:websocket` or `price:check-update` run.

### 7.2 Fiat currencies (~180 total, ~21 active)

Full list from gamba's `FiatCurrencySeeder` translated to Go structs.

Active: USD, EUR, BRL, GBP, JPY, CAD, AUD, CHF, CNY, INR, IDR, KRW, MXN, DKK, NZD, PHP, RUB, PEN, PLN, VND, TRY, ARS, NGN.

USD is always active and its `current_price` is always `1`.

---

## 8. Frontend

### 8.1 Types — `src/types/currency.ts`

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
```

### 8.2 Hook — `src/hooks/useCurrencies.ts`

Fetches `GET /v1/currencies` via SWR (refreshes every 30s). Builds:

- `cryptoToUsd: Record<string, number>` — `{ BTC: 65000, ETH: 3200 }`
- `usdToFiat: Record<string, number>` — `{ BRL: 5.10, EUR: 0.92 }` (computed as `1 / currency.current_price`)
- `convert(amount, fromCode, toCode)` — client-side conversion
- `convertCryptoToFiat(amount, cryptoCode, fiatCode)` — `amount * cryptoToUsd[crypto] * usdToFiat[fiat]`
- `formatFiat(amount, fiatCode)` — formats with symbol and correct decimal places

### 8.3 Hook — `src/hooks/usePreferences.ts`

Reads preferences from the user object (already fetched via `useMe()`). Provides:

- `preferredFiatCode` (default "USD")
- `displayInFiat` (default true)
- `updatePreferences(patch)` — calls `PUT /v1/me/preferences`
- `activeFiatSymbol` — looked up from currencies list

### 8.4 Fiat Selector Component — `src/components/Settings/CurrencyPreferenceTab.tsx`

Radio grid of active fiat currencies (code + symbol), like gamba's `PreferenceCurrency.vue`. Toggle for "Display balances in fiat". Save button calls `updatePreferences()`.

Added as a new tab in `/dashboard/settings`.

### 8.5 Modified Components

| Component | Change |
|-----------|--------|
| `AssetsByWallets.tsx` | Use `convertCryptoToFiat()` + `formatFiat()` instead of hardcoded `$` + `balance_usd` |
| `AssetsByAssets.tsx` | Portfolio totals in preferred fiat |
| `BalancesTable.tsx` | Show fiat equivalent per row |
| `User` type | Add `preferences?: UserPreferences` |

---

## 9. Data Flow

```
┌─────────────────────────────────────────────────────────┐
│                    PRICE SOURCES                         │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  CoinAPI WebSocket ──→ price:websocket command           │
│       (real-time)       │                               │
│                         ▼                               │
│                    ┌─────────┐    ┌───────┐             │
│                    │   DB    │◄──►│ Redis │             │
│                    │currencies│    │ cache │             │
│                    └────┬────┘    └───┬───┘             │
│                         │            │                  │
│  price:check-update ────┘            │                  │
│  (every 1 min)                       │                  │
│    ├─ CoinGecko REST                 │                  │
│    ├─ CoinMarketCap REST             │                  │
│    └─ CoinAPI REST                   │                  │
│                                      │                  │
├──────────────────────────────────────┼──────────────────┤
│                    API LAYER         │                  │
│                                      │                  │
│  GET /v1/currencies ─────────────────┘                  │
│  GET /v1/me/preferences ──→ users.preferences (JSONB)   │
│  PUT /v1/me/preferences ──→ merge into JSONB            │
│  GET /v1/convert ──→ PriceService.Convert()             │
│                                                         │
├─────────────────────────────────────────────────────────┤
│                    FRONTEND                             │
│                                                         │
│  useCurrencies() ──→ SWR fetch /v1/currencies (30s)     │
│    └─ cryptoToUsd, usdToFiat, convert(), formatFiat()   │
│                                                         │
│  usePreferences() ──→ user.preferences                  │
│    └─ preferredFiatCode, displayInFiat                  │
│                                                         │
│  Display: amount * cryptoToUsd[X] * usdToFiat[fiat]    │
│  e.g. 0.5 BTC * 65000 * 5.10 = R$ 165,750.00          │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## 10. New File Map

### Backend — New Files

| File | Purpose |
|------|---------|
| `app/models/currency.go` | Currency model |
| `app/models/user_preferences.go` | UserPreferences struct + helpers |
| `app/repositories/currency_repository.go` | CurrencyRepository interface + impl |
| `app/services/price/provider.go` | PriceProvider interface |
| `app/services/price/coingecko.go` | CoinGecko REST provider |
| `app/services/price/coinmarketcap.go` | CoinMarketCap REST provider |
| `app/services/price/coinapi.go` | CoinAPI REST provider |
| `app/services/price/service.go` | Orchestration, caching, fallback chain |
| `app/services/price/convert.go` | Conversion helpers |
| `app/services/price/websocket.go` | CoinAPI WebSocket client |
| `app/console/commands/price_websocket.go` | `price:websocket` artisan command |
| `app/console/commands/price_check_update.go` | `price:check-update` failover command |
| `app/http/controllers/currency_controller.go` | Currency endpoints |
| `app/http/controllers/preferences_controller.go` | User preferences endpoints |
| `database/migrations/YYYYMMDD_create_currencies_table.go` | Schema |
| `database/migrations/YYYYMMDD_add_preferences_to_users.go` | Schema |
| `database/seeders/currency_seeder.go` | Seeder registration |
| `database/seeds/currencies.go` | Crypto + fiat seed data |

### Backend — Modified Files

| File | Change |
|------|--------|
| `app/models/user.go` | Add `Preferences *UserPreferences` field |
| `app/container/container.go` | Add `CurrencyRepo`, `PriceService` |
| `app/providers/vault_container.go` | Wire currency repo + price service + providers |
| `bootstrap/app.go` | Register commands + currency seeder |
| `main.go` | Add `price_updater` Lambda mode |
| `routes/api.go` | Add `/v1/currencies`, `/v1/me/preferences`, `/v1/convert` |
| `.env.dev.example` | Add `COINGECKO_API_KEY`, `COINMARKETCAP_API_KEY`, `COINAPI_API_KEY` |
| `config/vault.go` | Add price provider config keys |

### Frontend — New Files

| File | Purpose |
|------|---------|
| `src/types/currency.ts` | Currency + UserPreferences types |
| `src/hooks/useCurrencies.ts` | SWR fetch + conversion helpers |
| `src/hooks/usePreferences.ts` | Preference state + update |
| `src/components/Settings/CurrencyPreferenceTab.tsx` | Fiat selector UI |

### Frontend — Modified Files

| File | Change |
|------|--------|
| `src/types/user.ts` | Add `preferences?: UserPreferences` |
| `src/components/Assets/AssetsByWallets.tsx` | Preferred fiat display |
| `src/components/Assets/AssetsByAssets.tsx` | Portfolio totals in preferred fiat |
| `src/components/Wallets/BalancesTable.tsx` | Fiat equivalent per row |
| `src/pages/dashboard/settings/index.tsx` | Add currency preference tab |

---

## 11. Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `COINAPI_API_KEY` | Yes (for WS) | — | CoinAPI WebSocket + REST key |
| `COINGECKO_API_KEY` | No | — | CoinGecko API key (free tier works without) |
| `COINMARKETCAP_API_KEY` | Yes (for fallback) | — | CoinMarketCap REST key |

---

## 12. Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| CoinAPI WebSocket disconnects | Exponential backoff reconnect + `price:check-update` failover every minute |
| All REST providers fail | Log warning; prices remain stale but functional; frontend shows last known price |
| Rate limits on free tiers | CoinGecko free = 10-30 req/min; batch codes in single request; cache aggressively |
| JSONB preferences migration on existing users | Default `{}`, code handles nil/missing gracefully |
| Provider API changes | Interface abstraction isolates each provider; only one file changes |
