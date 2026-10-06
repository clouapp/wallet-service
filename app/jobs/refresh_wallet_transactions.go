package jobs

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

const refreshWalletTransactionsJob = "refresh_wallet_transactions"

type RefreshWalletTransactions struct {
	wallets walletQueue
}

// NewRefreshWalletTransactions refreshes one wallet's transactions.
func NewRefreshWalletTransactions(refresher *refresh.WalletRefresher) *RefreshWalletTransactions {
	if refresher == nil {
		panic("refresh_wallet_transactions: wallet refresher is required")
	}
	return &RefreshWalletTransactions{wallets: refresher}
}

func (j *RefreshWalletTransactions) Signature() string {
	return refreshWalletTransactionsJob
}

func (j *RefreshWalletTransactions) Handle(args ...any) error {
	payload, err := decodeWalletPayload(refreshWalletTransactionsJob, args)
	if err != nil {
		return err
	}
	return j.wallets.RefreshTransactions(context.Background(), payload.WalletID, payload.ChainID)
}

func (j *RefreshWalletTransactions) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	return shouldRetry(err, attempt)
}
