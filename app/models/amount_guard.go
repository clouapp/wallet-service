package models

import (
	"github.com/macrowallets/waas/pkg/amount"
)

// Amount columns that are stored as absolute values. A negative value is rejected
// before the write; the matching CHECK constraints (migration 280) back this up.
var (
	TransactionAmountColumns     = []string{"amount", "fee"}
	WithdrawalAmountColumns      = []string{"amount", "fee_estimate"}
	WalletBalanceAmountColumns   = []string{"balance_raw", "balance_display"}
	AssetBalanceAmountColumns    = []string{"amount_raw", "amount_display"}
	UTXOAmountColumns            = []string{"value_raw"}
	SnapshotBalanceAmountColumns = []string{"balance_raw", "balance_display"}
)

func (t *Transaction) ValidateAmounts() error {
	if err := amount.RequireNonNegative("transactions.amount", t.Amount); err != nil {
		return err
	}
	return amount.RequireNonNegative("transactions.fee", t.Fee)
}

func (w *Withdrawal) ValidateAmounts() error {
	if err := amount.RequireNonNegative("withdrawals.amount", w.Amount); err != nil {
		return err
	}
	return amount.RequireNonNegative("withdrawals.fee_estimate", w.FeeEstimate)
}

func (b *WalletAssetBalance) ValidateAmounts() error {
	if err := amount.RequireNonNegative("wallet_asset_balances.amount_raw", b.AmountRaw); err != nil {
		return err
	}
	return amount.RequireNonNegative("wallet_asset_balances.amount_display", b.AmountDisplay)
}

func (s *WalletBalanceSnapshot) ValidateAmounts() error {
	if err := amount.RequireNonNegative("wallet_balance_snapshots.balance_raw", s.BalanceRaw); err != nil {
		return err
	}
	return amount.RequireNonNegative("wallet_balance_snapshots.balance_display", s.BalanceDisplay)
}

func (u *WalletUTXO) ValidateAmounts() error {
	return amount.RequireNonNegative("wallet_utxos.value_raw", u.ValueRaw)
}
