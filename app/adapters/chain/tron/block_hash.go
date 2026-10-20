package tron

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.BlockHashReader = (*TronLive)(nil)

// BlockHashAt is the block id (lowercase hex) of the block the node holds at
// height, read from its header (/wallet/getblock without transactions, falling
// back to /wallet/getblockbynum on nodes that predate it). An answer without that
// block (past the node's head) is an error.
func (a *TronLive) BlockHashAt(ctx context.Context, height uint64) (string, error) {
	var block tronBlockHeaderJSON
	err := a.post(ctx, "/wallet/getblock", map[string]any{"id_or_num": strconv.FormatUint(height, 10), "detail": false}, &block)
	var statusErr *tronStatusError
	if errors.As(err, &statusErr) {
		block = tronBlockHeaderJSON{}
		err = a.post(ctx, "/wallet/getblockbynum", map[string]any{"num": height}, &block)
	}
	if err != nil {
		return "", fmt.Errorf("tron %s block %d hash: %w", a.cfg.ChainIDStr, height, err)
	}
	head, err := parseTronBlockHeader(block, height)
	if err != nil {
		return "", fmt.Errorf("tron %s block %d hash: %w", a.cfg.ChainIDStr, height, err)
	}
	return hex.EncodeToString(head.blockID), nil
}
