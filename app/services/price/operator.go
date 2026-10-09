package price

import (
	"context"
	"fmt"
	"time"

	contractscache "github.com/goravel/framework/contracts/cache"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/security"
)

// CheckOutput is what price:check-update prints before a failure.
type CheckOutput struct {
	Info []string
}

// LogFunc receives a line while a long-running price command is still inside
// its one service call. level is "info" or "error".
type LogFunc func(level, message string)

// CheckUpdate refreshes stale crypto and fiat prices.
func (s *Service) CheckUpdate(ctx context.Context) (CheckOutput, error) {
	var out CheckOutput
	if s == nil {
		return out, fmt.Errorf("price service is not initialized")
	}
	staleCryptos, err := s.FindStale(ctx, models.CurrencyTypeCrypto, 1*time.Minute)
	if err != nil {
		return out, fmt.Errorf("failed to check stale cryptos: %w", err)
	}
	if len(staleCryptos) > 0 {
		out.Info = append(out.Info, fmt.Sprintf("found %d stale crypto currencies, refreshing...", len(staleCryptos)))
		if err := s.RefreshCryptoPrices(ctx); err != nil {
			return out, fmt.Errorf("crypto refresh failed: %w", err)
		}
		stillStale, err := s.FindStale(ctx, models.CurrencyTypeCrypto, 1*time.Minute)
		if err != nil {
			return out, fmt.Errorf("failed to check stale cryptos: %w", err)
		}
		if len(stillStale) > 0 {
			codes := make([]string, len(stillStale))
			for i, currency := range stillStale {
				codes[i] = currency.Code
			}
			return out, fmt.Errorf("still stale after all providers: %v", codes)
		}
		out.Info = append(out.Info, "all crypto prices refreshed successfully")
	} else {
		out.Info = append(out.Info, "all crypto prices are up to date")
	}

	staleFiats, err := s.FindStale(ctx, models.CurrencyTypeFiat, 1*time.Hour)
	if err != nil {
		return out, fmt.Errorf("failed to check stale fiats: %w", err)
	}
	if len(staleFiats) > 0 {
		out.Info = append(out.Info, fmt.Sprintf("found %d stale fiat currencies, refreshing...", len(staleFiats)))
		if err := s.RefreshFiatRates(ctx); err != nil {
			return out, fmt.Errorf("fiat refresh failed: %w", err)
		}
	} else {
		out.Info = append(out.Info, "all fiat rates are up to date")
	}
	return out, nil
}

// Stream refreshes prices once, then holds the CoinAPI socket.
func (s *Service) Stream(ctx context.Context, apiKey string, cache contractscache.Driver, log LogFunc) error {
	if log == nil {
		log = func(string, string) {}
	}
	log("info", "refreshing initial prices...")
	if s != nil {
		if err := s.RefreshCryptoPrices(ctx); err != nil {
			log("error", security.RedactText("initial crypto refresh failed: "+err.Error()))
		}
		if err := s.RefreshFiatRates(ctx); err != nil {
			log("error", security.RedactText("initial fiat refresh failed: "+err.Error()))
		}
	}
	log("info", "initial prices refreshed")
	if apiKey == "" {
		return fmt.Errorf("COINAPI_API_KEY is not configured")
	}
	if s == nil {
		return fmt.Errorf("price service is not initialized")
	}
	log("info", "starting CoinAPI WebSocket connection...")
	return s.PriceWebSocket(apiKey, cache).Connect(ctx)
}
