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

	priceMap := make(map[string]float64, len(cryptos))
	for _, c := range cryptos {
		priceMap[c.Code] = c.CurrentPrice
	}

	for _, provider := range s.providers {
		staleCodes := findStaleCodes(cryptos)
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

		cryptos, _ = s.currencyRepo.FindActiveCryptos()
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
