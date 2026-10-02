package apitoken

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
)

// ReserveDailyUSD counts this withdrawal against the token's daily USD cap.
// A token with no cap returns nil without touching Redis. When a cap is set,
// a missing Redis client or an unpriced asset refuses the withdrawal.
func ReserveDailyUSD(ctx context.Context, rdb *redis.Client, key, limitJSON, asset, amount string) error {
	if _, limited, err := models.TokenDailyUSDLimit(limitJSON); err != nil {
		return err
	} else if !limited {
		return nil
	}
	if rdb == nil {
		return fmt.Errorf("spending limit unavailable")
	}
	if key == "" {
		return fmt.Errorf("spending limit key is required")
	}
	add, err := models.TokenUSDAmount(asset, amount)
	if err != nil {
		return err
	}
	spent, err := rdb.IncrByFloat(ctx, key, add).Result()
	if err != nil {
		return fmt.Errorf("spending limit counter: %w", err)
	}
	if spent == add {
		_ = rdb.Expire(ctx, key, 48*time.Hour).Err()
	}
	if _, err := models.AuthorizeTokenSpend(limitJSON, asset, amount, spent-add); err != nil {
		_ = rdb.IncrByFloat(ctx, key, -add).Err()
		return err
	}
	return nil
}
