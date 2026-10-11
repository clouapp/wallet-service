package tron

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/services/chain"
)

var (
	_ chain.SolidifiedBlockReader  = (*TronLive)(nil)
	_ chain.FinalityPolicyReporter = (*TronLive)(nil)
)

// FinalityPolicy is the chain row's finality policy (chains.finality).
func (a *TronLive) FinalityPolicy() string { return a.cfg.Finality }

// SolidifiedBlock is the number of the newest solidified block, read from the
// solidity API (/walletsolidity/getblock without transactions, falling back to
// /walletsolidity/getnowblock on nodes that predate it). TronGrid serves it on the
// same host; a java-tron full node serves it on its solidity port, so an RPC URL
// without it fails here. The header is checked like the head block's.
func (a *TronLive) SolidifiedBlock(ctx context.Context) (uint64, error) {
	var block tronBlockHeaderJSON
	err := a.post(ctx, "/walletsolidity/getblock", map[string]any{"detail": false}, &block)
	var statusErr *tronStatusError
	if errors.As(err, &statusErr) {
		block = tronBlockHeaderJSON{}
		err = a.post(ctx, "/walletsolidity/getnowblock", map[string]any{}, &block)
	}
	if err != nil {
		return 0, fmt.Errorf("tron %s solidified block: %w", a.cfg.ChainIDStr, err)
	}
	head, err := parseTronBlockHeader(block, 0)
	if err != nil {
		return 0, fmt.Errorf("tron %s solidified block: %w", a.cfg.ChainIDStr, err)
	}
	if head.number <= 0 {
		return 0, fmt.Errorf("tron %s solidified block has no number", a.cfg.ChainIDStr)
	}
	return uint64(head.number), nil
}
