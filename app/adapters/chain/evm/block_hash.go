package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.BlockHashReader = (*EVMLive)(nil)

// BlockHashAt is the hash (lowercase) of the block the node holds at height now,
// read from its header (eth_getBlockByNumber without transactions). A height no
// endpoint returns a block for (a lagging node) is an error, never an empty hash.
func (a *EVMLive) BlockHashAt(ctx context.Context, height uint64) (string, error) {
	var header *struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := a.rpc.Call(ctx, "eth_getBlockByNumber", &header, fmt.Sprintf("0x%x", height), false); err != nil {
		return "", fmt.Errorf("evm %s block %d hash: %w", a.cfg.ChainIDStr, height, err)
	}
	if header == nil || strings.TrimSpace(header.Hash) == "" {
		return "", fmt.Errorf("evm %s: no endpoint returned block %d", a.cfg.ChainIDStr, height)
	}
	number, err := parseHexQuantity(header.Number, "block number")
	if err != nil || !number.IsUint64() || number.Uint64() != height {
		return "", fmt.Errorf("evm %s: asked for block %d, the node answered block %q", a.cfg.ChainIDStr, height, header.Number)
	}
	return strings.ToLower(strings.TrimSpace(header.Hash)), nil
}
