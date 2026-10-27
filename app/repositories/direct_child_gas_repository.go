package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// WithinDirectChildGas serializes wallet nonce allocation and an operation's
// immutable seed row. A caller's outer transaction is refused: returning before
// its physical commit could otherwise transmit uncommitted signed bytes.
// Network transmission and receipt waits always happen after this returns.
func (r *TransactionRepository) WithinDirectChildGas(ctx context.Context, walletID, stepID uuid.UUID, fn func(context.Context) error) error {
	if walletID == uuid.Nil || stepID == uuid.Nil || fn == nil {
		return fmt.Errorf("direct-child gas: wallet, step and callback are required")
	}
	if r.Query(ctx).InTransaction() {
		return fmt.Errorf("direct-child gas: an outer transaction is not permitted")
	}
	return r.Within(ctx, func(txCtx context.Context) error {
		var state struct {
			ReadOnly string `gorm:"column:transaction_read_only"`
		}
		if err := r.Query(txCtx).Raw("SHOW transaction_read_only").Scan(&state); err != nil {
			return fmt.Errorf("direct-child gas writer: %w", err)
		}
		if state.ReadOnly != "off" {
			return fmt.Errorf("direct-child gas requires a writable transaction")
		}
		for _, key := range []string{"direct-child-gas/wallet/" + walletID.String(), "direct-child-gas/step/" + stepID.String()} {
			if _, err := r.Query(txCtx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key); err != nil {
				return fmt.Errorf("lock direct-child gas: %w", err)
			}
		}
		if _, err := NewWalletRepository(nil).LockByID(txCtx, walletID); err != nil {
			return err
		}
		return fn(txCtx)
	})
}

// DirectChildGasNonceOccupied prevents a different committed preparation from
// signing the same base nonce before its earlier bytes reach a node. It runs
// under the wallet advisory lock, not a cache lease. Final rows keep ownership.
func (r *TransactionRepository) DirectChildGasNonceOccupied(ctx context.Context, exceptID uuid.UUID, chainID, from string, nonce uint64) (bool, error) {
	if !r.Query(ctx).InTransaction() {
		return false, fmt.Errorf("direct-child gas nonce check requires its transaction")
	}
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := r.Query(ctx).Raw(`SELECT COUNT(*) AS count FROM transactions
		WHERE id <> ? AND chain = ? AND lower(from_address) = lower(?)
		AND tx_type IN (?, ?, ?) AND raw_payload->>'sender_nonce' = ?`,
		exceptID, chainID, from, models.TxTypeGasSeed, models.TxTypeWithdrawal, models.TxTypeSweep, fmt.Sprint(nonce)).Scan(&row)
	if err != nil {
		return false, fmt.Errorf("read direct-child gas nonce ownership: %w", err)
	}
	return row.Count > 0, nil
}
