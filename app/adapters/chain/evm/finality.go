package evm

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/services/chain"
)

var (
	_ chain.FinalizedBlockReader   = (*EVMLive)(nil)
	_ chain.FinalityPolicyReporter = (*EVMLive)(nil)
)

// errNoFinalizedBlock is a node that answers the "finalized" tag without a block:
// a chain or client without finality, or one that has not finalized a block yet.
var errNoFinalizedBlock = errors.New("evm node reports no finalized block")

// FinalityPolicy is the chain row's finality policy (chains.finality).
func (a *EVMLive) FinalityPolicy() string { return a.cfg.Finality }

// FinalizedBlock is the number of the block eth_getBlockByNumber("finalized")
// returns. A node without a finalized block (null, no number, or genesis) is an
// error, never block 0.
func (a *EVMLive) FinalizedBlock(ctx context.Context) (uint64, error) {
	var header *struct {
		Number string `json:"number"`
	}
	if err := a.rpc.Call(ctx, "eth_getBlockByNumber", &header, "finalized", false); err != nil {
		return 0, fmt.Errorf("evm %s finalized block: %w", a.cfg.ChainIDStr, err)
	}
	if header == nil {
		return 0, fmt.Errorf("evm %s: %w", a.cfg.ChainIDStr, errNoFinalizedBlock)
	}
	number, err := parseHexQuantity(header.Number, "finalized block number")
	if err != nil {
		return 0, fmt.Errorf("evm %s: %w", a.cfg.ChainIDStr, err)
	}
	if number.Sign() == 0 || !number.IsUint64() {
		return 0, fmt.Errorf("evm %s: %w (number %s)", a.cfg.ChainIDStr, errNoFinalizedBlock, header.Number)
	}
	return number.Uint64(), nil
}
