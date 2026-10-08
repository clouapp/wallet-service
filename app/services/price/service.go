package price

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	contractscache "github.com/goravel/framework/contracts/cache"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

// currencyStore is the price rows this package reads and updates.
type currencyStore interface {
	FindByCode(ctx context.Context, code string) (*models.Currency, error)
	FindActiveCryptos(ctx context.Context) ([]models.Currency, error)
	FindActiveFiats(ctx context.Context) ([]models.Currency, error)
	FindStale(ctx context.Context, currencyType string, staleDuration time.Duration) ([]models.Currency, error)
	SetPrice(ctx context.Context, code string, currentPrice, lastPrice decimal.Decimal) error
}

const (
	redisCurrencyTTL = 60 * time.Second
	usdCode          = "USD"
)

var usdPrice = decimal.NewFromInt(1)

// ErrPriceNotQuoted marks a currency whose stored price no provider ever quoted.
var ErrPriceNotQuoted = errors.New("price was never quoted by a provider")

type Service struct {
	providers    []PriceProvider
	currencyRepo currencyStore
	cache        contractscache.Driver
	quotes       QuoteDialer
	settings     SettingsSource
	envCoinAPI   string
	newProvider  quoteProviderFactory
}

// Deps is everything the price service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Providers  []PriceProvider
	Currencies currencyStore
	Cache      contractscache.Driver
}

// NewService wires the price service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		providers:    deps.Providers,
		currencyRepo: deps.Currencies,
		cache:        deps.Cache,
	}
}

func (s *Service) RefreshCryptoPrices(ctx context.Context) error {
	cryptos, err := s.currencyRepo.FindActiveCryptos(ctx)
	if err != nil {
		return fmt.Errorf("load active cryptos: %w", err)
	}
	if len(cryptos) == 0 {
		return nil
	}

	priceMap := make(map[string]decimal.Decimal, len(cryptos))
	for _, c := range cryptos {
		priceMap[c.Code] = c.CurrentPrice.Decimal
	}

	for _, provider := range s.providersForQuote(ctx) {
		staleCodes := findStaleCodes(cryptos)
		if len(staleCodes) == 0 {
			break
		}

		prices, err := provider.FetchCryptoPrices(ctx, staleCodes)
		if err != nil {
			slog.Warn("crypto price fetch failed", "provider", provider.Name(), "error", err)
			continue
		}

		for code, quoted := range prices {
			newPrice, ok := fitQuotedPrice(code, quoted)
			if !ok {
				continue
			}
			oldPrice := priceMap[code]
			if err := s.currencyRepo.SetPrice(ctx, code, newPrice, oldPrice); err != nil {
				slog.Warn("update crypto price failed", "code", code, "error", err)
				continue
			}
			priceMap[code] = newPrice
			s.cachePrice(ctx, code, newPrice)
			slog.Info("crypto price updated", "provider", provider.Name(), "code", code, "price", newPrice.String())
		}

		cryptos, _ = s.currencyRepo.FindActiveCryptos(ctx)
	}
	return nil
}

func (s *Service) RefreshFiatRates(ctx context.Context) error {
	fiats, err := s.currencyRepo.FindActiveFiats(ctx)
	if err != nil {
		return fmt.Errorf("load active fiats: %w", err)
	}

	codes := make([]string, 0, len(fiats))
	for _, f := range fiats {
		if f.Code == usdCode {
			continue
		}
		codes = append(codes, f.Code)
	}
	if len(codes) == 0 {
		return nil
	}

	for _, provider := range s.providersForQuote(ctx) {
		rates, err := provider.FetchFiatRates(ctx, codes)
		if err != nil {
			slog.Warn("fiat rate fetch failed", "provider", provider.Name(), "error", err)
			continue
		}
		if len(rates) == 0 {
			continue
		}

		for code, quoted := range rates {
			rate, ok := fitQuotedPrice(code, quoted)
			if !ok {
				continue
			}
			oldRate := decimal.Zero
			for _, f := range fiats {
				if f.Code == code {
					oldRate = f.CurrentPrice.Decimal
					break
				}
			}
			if err := s.currencyRepo.SetPrice(ctx, code, rate, oldRate); err != nil {
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

// FindStale returns the currency store's stale rows for currencyType.
func (s *Service) FindStale(ctx context.Context, currencyType string, staleDuration time.Duration) ([]models.Currency, error) {
	return s.currencyRepo.FindStale(ctx, currencyType, staleDuration)
}

// WithQuoteDialer installs the CoinAPI socket opener. The provider supplies it;
// this package never imports the websocket library.
func (s *Service) WithQuoteDialer(dialer QuoteDialer) *Service {
	if s == nil {
		return nil
	}
	s.quotes = dialer
	return s
}

// PriceWebSocket streams CoinAPI quotes through the currency rows this service updates.
// cache may be nil when Redis is not configured; it is not the service's own cache.
func (s *Service) PriceWebSocket(apiKey string, cache contractscache.Driver) *WebSocketClient {
	if s == nil {
		return nil
	}
	return NewWebSocketClient(WebSocketClientDeps{
		APIKey:     apiKey,
		Currencies: s.currencyRepo,
		Cache:      cache,
		Dialer:     s.quotes,
	})
}

// GetPrice returns the USD price of code: the cached quote when present, otherwise
// the stored one.
func (s *Service) GetPrice(ctx context.Context, code string) (decimal.Decimal, error) {
	if strings.TrimSpace(code) == "" {
		return decimal.Decimal{}, fmt.Errorf("currency code is required")
	}
	if code == usdCode {
		return usdPrice, nil
	}

	if cached, ok := s.cachedPrice(ctx, code); ok {
		return cached, nil
	}

	cur, err := s.currencyRepo.FindByCode(ctx, code)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		cur, err = nil, nil
	}
	if err != nil {
		return decimal.Decimal{}, err
	}
	if cur == nil {
		return decimal.Decimal{}, fmt.Errorf("currency not found: %s", code)
	}
	return cur.CurrentPrice.Decimal, nil
}

// QuotedUSDPrice is GetPrice restricted to prices a provider quoted: the cached
// quote, or a stored price with a price_updated_at. A currency still at the
// column default was never priced and answers ErrPriceNotQuoted.
func (s *Service) QuotedUSDPrice(ctx context.Context, code string) (decimal.Decimal, error) {
	if strings.TrimSpace(code) == "" {
		return decimal.Decimal{}, fmt.Errorf("currency code is required")
	}
	if code == usdCode {
		return usdPrice, nil
	}
	if cached, ok := s.cachedPrice(ctx, code); ok {
		return cached, nil
	}
	cur, err := s.currencyRepo.FindByCode(ctx, code)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if cur == nil {
		return decimal.Decimal{}, fmt.Errorf("currency not found: %s", code)
	}
	if cur.PriceUpdatedAt == nil || !cur.CurrentPrice.Decimal.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("%w: %s", ErrPriceNotQuoted, code)
	}
	return cur.CurrentPrice.Decimal, nil
}

func (s *Service) UpdateSinglePrice(ctx context.Context, code string, newPrice decimal.Decimal) error {
	fitted, ok := fitQuotedPrice(code, newPrice)
	if !ok {
		return fmt.Errorf("currency %s price %s is not a storable positive price", code, newPrice.String())
	}
	cur, err := s.currencyRepo.FindByCode(ctx, code)
	if err != nil || cur == nil {
		return fmt.Errorf("currency not found: %s", code)
	}
	oldPrice := cur.CurrentPrice.Decimal
	if err := s.currencyRepo.SetPrice(ctx, code, fitted, oldPrice); err != nil {
		return err
	}
	s.cachePrice(ctx, code, fitted)
	return nil
}

// cachedPrice reads a cached quote; a missing, unreadable or non-positive entry is
// a cache miss.
func (s *Service) cachedPrice(ctx context.Context, code string) (decimal.Decimal, bool) {
	if s.cache == nil {
		return decimal.Decimal{}, false
	}
	text := s.cache.GetString("currency:"+code, "")
	if text == "" {
		return decimal.Decimal{}, false
	}
	cached, err := numeric.Parse("cached price of "+code, text)
	if err != nil || !cached.IsPositive() {
		return decimal.Decimal{}, false
	}
	return cached, true
}

func (s *Service) cachePrice(ctx context.Context, code string, price decimal.Decimal) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Put("currency:"+code, price.String(), redisCurrencyTTL); err != nil {
		slog.Warn("cache currency failed", "code", code, "error", err)
	}
}

// fitQuotedPrice rounds a provider quote to the price column; quotes that are not
// positive or do not fit are skipped.
func fitQuotedPrice(code string, quoted decimal.Decimal) (decimal.Decimal, bool) {
	fitted, err := models.CurrencyPriceColumn.Fit(quoted)
	if err != nil {
		slog.Warn("skipping price quote", "code", code, "price", quoted.String(), "error", err)
		return decimal.Decimal{}, false
	}
	if !fitted.IsPositive() {
		return decimal.Decimal{}, false
	}
	return fitted, true
}

func findStaleCodes(currencies []models.Currency) []string {
	stale := make([]string, 0)
	cutoff := time.Now().Add(-1 * time.Minute)
	for _, c := range currencies {
		if c.PriceUpdatedAt == nil || c.PriceUpdatedAt.Before(cutoff) {
			stale = append(stale, c.Code)
		}
	}
	return stale
}
