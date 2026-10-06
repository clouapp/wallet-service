package jobs

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

const refreshWalletTokensJob = "refresh_wallet_tokens"

type RefreshWalletTokens struct {
	wallets walletQueue
}

// NewRefreshWalletTokens refreshes one wallet's token balances.
func NewRefreshWalletTokens(refresher *refresh.WalletRefresher) *RefreshWalletTokens {
	if refresher == nil {
		panic("refresh_wallet_tokens: wallet refresher is required")
	}
	return &RefreshWalletTokens{wallets: refresher}
}

func (j *RefreshWalletTokens) Signature() string {
	return refreshWalletTokensJob
}

func (j *RefreshWalletTokens) Handle(args ...any) error {
	payload, err := decodeWalletPayload(refreshWalletTokensJob, args)
	if err != nil {
		return err
	}
	return j.wallets.RefreshTokens(context.Background(), payload.WalletID, payload.ChainID)
}

func (j *RefreshWalletTokens) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 {
		return false, 0
	}
	return true, time.Duration(attempt) * 5 * time.Second
}
