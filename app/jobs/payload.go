package jobs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// walletPayload is the decoded wallet id and chain id a refresh job accepts.
type walletPayload struct {
	WalletID uuid.UUID
	ChainID  string
}

// walletQueue is the one service each wallet job calls after decode.
type walletQueue interface {
	RefreshBalances(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshTransactions(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshTokens(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshUTXOs(ctx context.Context, walletID uuid.UUID, chainID string) error
	ReconcileWallet(ctx context.Context, walletID uuid.UUID, chainID string) error
}

func decodeWalletPayload(job string, args []any) (walletPayload, error) {
	if len(args) < 2 {
		return walletPayload{}, fmt.Errorf("%s: expected 2 args (wallet_id, chain_id)", job)
	}
	walletIDStr, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletIDStr == "" {
		return walletPayload{}, fmt.Errorf("%s: wallet_id must be a non-empty string", job)
	}
	if !ok1 || chainID == "" {
		return walletPayload{}, fmt.Errorf("%s: chain_id must be a non-empty string", job)
	}
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return walletPayload{}, fmt.Errorf("%s: invalid wallet_id: %w", job, err)
	}
	return walletPayload{WalletID: walletID, ChainID: chainID}, nil
}
