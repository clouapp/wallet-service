package withdraw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/macrowallets/waas/app/models"
	"time"
)

// AuthorizeRampFunding applies the existing API-token USD budget to an exact
// stored funding order. It reserves once across action renewal/retries. A cache
// failure never authorizes a signature; a crash after counting may conservatively
// count again, as no unrecorded reservation is assumed to have succeeded.
func (s *Service) AuthorizeRampFunding(ctx context.Context, token *models.AccessToken, orderID, asset, amount string) error {
	if token == nil {
		return nil
	}
	_, set, err := dailyLimitCents(token.SpendingLimit)
	if err != nil {
		return err
	}
	if !set {
		return nil
	}
	if s == nil || s.cache == nil || orderID == "" {
		return ErrSpendingLimitInvalid
	}
	key := "ramp-budget:" + token.ID.String() + ":" + orderID
	release, err := s.holdIdempotencyKey(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	digest := sha256.Sum256([]byte(asset + ":" + amount))
	binding := hex.EncodeToString(digest[:])
	previous := s.cache.GetString(key)
	if previous != "" {
		if previous != binding {
			return ErrSpendingLimitInvalid
		}
		return nil
	}
	req := WithdrawRequest{Asset: asset}.WithAPIToken(token, amount)
	if err := s.enforceTokenSpendingLimit(ctx, req); err != nil {
		return err
	}
	if err := s.cache.Put(key, binding, 30*24*time.Hour); err != nil {
		return fmt.Errorf("record ramp budget reservation: %w", err)
	}
	return nil
}
