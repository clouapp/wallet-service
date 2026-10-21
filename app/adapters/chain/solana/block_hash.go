package solana

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.BlockHashReader = (*SolanaLive)(nil)

// BlockHashAt is the blockhash of the finalized block at slot (getBlock without
// transactions or rewards). The scanner records only finalized slots, so a slot
// answered as skipped, or a null block, is an error here, never a missing block.
func (a *SolanaLive) BlockHashAt(ctx context.Context, slot uint64) (string, error) {
	var block struct {
		Blockhash string `json:"blockhash"`
	}
	err := a.rpc.Call(ctx, "getBlock", &block, slot, map[string]interface{}{
		"encoding":                       "json",
		"transactionDetails":             "none",
		"rewards":                        false,
		"commitment":                     solanaCommitmentFinalized,
		"maxSupportedTransactionVersion": solanaMaxSupportedTxVersion,
	})
	if err != nil {
		return "", fmt.Errorf("solana %s slot %d blockhash: %w", a.cfg.ChainIDStr, slot, err)
	}
	if strings.TrimSpace(block.Blockhash) == "" {
		return "", fmt.Errorf("solana %s: no endpoint returned slot %d", a.cfg.ChainIDStr, slot)
	}
	return strings.TrimSpace(block.Blockhash), nil
}
