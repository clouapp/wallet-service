package jobs

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

const refreshWalletBalancesJob = "refresh_wallet_balances"

type RefreshWalletBalances struct {
	wallets walletQueue
}

// NewRefreshWalletBalances refreshes one wallet's balances.
func NewRefreshWalletBalances(refresher *refresh.WalletRefresher) *RefreshWalletBalances {
	if refresher == nil {
		panic("refresh_wallet_balances: wallet refresher is required")
	}
	return &RefreshWalletBalances{wallets: refresher}
}

func (j *RefreshWalletBalances) Signature() string {
	return refreshWalletBalancesJob
}

func (j *RefreshWalletBalances) Handle(args ...any) error {
	payload, err := decodeWalletPayload(refreshWalletBalancesJob, args)
	if err != nil {
		return err
	}
	return j.wallets.RefreshBalances(context.Background(), payload.WalletID, payload.ChainID)
}

func (j *RefreshWalletBalances) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
