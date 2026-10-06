package jobs

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

const refreshWalletUTXOsJob = "refresh_wallet_utxos"

type RefreshWalletUTXOs struct {
	wallets walletQueue
}

// NewRefreshWalletUTXOs refreshes one wallet's unspent outputs.
func NewRefreshWalletUTXOs(refresher *refresh.WalletRefresher) *RefreshWalletUTXOs {
	if refresher == nil {
		panic("refresh_wallet_utxos: wallet refresher is required")
	}
	return &RefreshWalletUTXOs{wallets: refresher}
}

func (j *RefreshWalletUTXOs) Signature() string {
	return refreshWalletUTXOsJob
}

func (j *RefreshWalletUTXOs) Handle(args ...any) error {
	payload, err := decodeWalletPayload(refreshWalletUTXOsJob, args)
	if err != nil {
		return err
	}
	return j.wallets.RefreshUTXOs(context.Background(), payload.WalletID, payload.ChainID)
}

func (j *RefreshWalletUTXOs) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
