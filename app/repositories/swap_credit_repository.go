package repositories

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/amount"
)

// ClaimCredit serializes allocations of one physical chain payout, including
// different orders receiving a batched payment at the same custody address. An
// allocation is the reported amount capped to what the payout still holds.
func (r *SwapRepository) ClaimCredit(ctx context.Context, credit *models.SwapCreditAllocation, readTotal func(context.Context) (string, error)) (string, bool, error) {
	claimed, confirmed := "0", false
	value, ok := amount.ParseBaseUnits(credit.AmountRaw)
	if !ok || value.Sign() <= 0 {
		return claimed, false, fmt.Errorf("invalid swap credit allocation")
	}
	err := r.Within(ctx, func(txCtx context.Context) error {
		key := strings.Join([]string{"swap-credit", credit.Chain, credit.TxHash, credit.AssetKey, credit.Address}, "|")
		if _, err := r.Query(txCtx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key); err != nil {
			return fmt.Errorf("lock swap credit: %w", err)
		}
		var existing models.SwapCreditAllocation
		err := r.Query(txCtx).Where("order_id = ? AND direction = ?", credit.OrderID, credit.Direction).FirstOrFail(&existing)
		if err == nil {
			if existing.Chain != credit.Chain || existing.AssetKey != credit.AssetKey || existing.Address != credit.Address || existing.TxHash != credit.TxHash || existing.WalletID != credit.WalletID {
				return fmt.Errorf("swap credit allocation conflicts with its recorded payout")
			}
			claimed, confirmed = existing.AmountRaw, true
			return nil
		}
		if !errors.Is(db.LookupError(err, "find swap allocation"), models.ErrRepositoryNotFound) {
			return err
		}
		totalRaw, err := readTotal(txCtx)
		if err != nil {
			return err
		}
		total, ok := amount.ParseBaseUnits(totalRaw)
		if !ok || total.Sign() < 0 {
			return fmt.Errorf("invalid confirmed swap credit total")
		}
		var allocations []models.SwapCreditAllocation
		if err := r.Query(txCtx).Where("chain = ? AND tx_hash = ? AND asset_key = ? AND address = ?", credit.Chain, credit.TxHash, credit.AssetKey, credit.Address).Find(&allocations); err != nil {
			return err
		}
		used := new(big.Int)
		for _, allocation := range allocations {
			units, ok := amount.ParseBaseUnits(allocation.AmountRaw)
			if !ok || units.Sign() <= 0 {
				return fmt.Errorf("invalid stored swap allocation")
			}
			used.Add(used, units)
		}
		// The credit is what the chain shows, never more: a report above what is
		// left of the payout (a rounded decimal, a batched payout) is capped to it.
		remaining := new(big.Int).Sub(total, used)
		if remaining.Sign() <= 0 {
			return nil
		}
		if value.Cmp(remaining) > 0 {
			credit.AmountRaw = remaining.String()
		}
		if err := r.Query(txCtx).Create(credit); err != nil {
			return fmt.Errorf("persist swap credit allocation: %w", err)
		}
		claimed, confirmed = credit.AmountRaw, true
		return nil
	})
	return claimed, confirmed, err
}
