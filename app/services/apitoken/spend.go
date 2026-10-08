package apitoken

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/app/models"
)

// DailyCounter is the namespaced float counter behind a token's daily USD cap.
type DailyCounter interface {
	IncrByFloat(ctx context.Context, key string, value float64) (float64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// ReserveDailyUSD counts this withdrawal against the token's daily USD cap.
// A token with no cap returns nil without touching the counter. When a cap is
// set, a missing counter or an unpriced asset refuses the withdrawal.
func ReserveDailyUSD(ctx context.Context, counter DailyCounter, key, limitJSON, asset, amount string) error {
	if _, limited, err := models.TokenDailyUSDLimit(limitJSON); err != nil {
		return err
	} else if !limited {
		return nil
	}
	if counter == nil {
		return fmt.Errorf("spending limit unavailable")
	}
	if key == "" {
		return fmt.Errorf("spending limit key is required")
	}
	add, err := models.TokenUSDAmount(asset, amount)
	if err != nil {
		return err
	}
	spent, err := counter.IncrByFloat(ctx, key, add)
	if err != nil {
		return fmt.Errorf("spending limit counter: %w", err)
	}
	if spent == add {
		_ = counter.Expire(ctx, key, 48*time.Hour)
	}
	if _, err := models.AuthorizeTokenSpend(limitJSON, asset, amount, spent-add); err != nil {
		_, _ = counter.IncrByFloat(ctx, key, -add)
		return err
	}
	return nil
}
