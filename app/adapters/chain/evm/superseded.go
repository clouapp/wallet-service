package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/pkg/types"
)

// minSupersededDepth is the fewest blocks under the head at which another
// transaction must already have spent a nonce before a transfer with that nonce
// is declared unable to land. A node a few blocks behind may not know a transfer
// that was just mined; it is very unlikely to be this far behind.
const minSupersededDepth = 12

// SenderNonce is the nonce the signed transaction spends.
func (a *EVMLive) SenderNonce(signed *types.SignedTx) (uint64, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return 0, fmt.Errorf("evm sender nonce: signed transaction is required")
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(signed.RawBytes); err != nil {
		return 0, fmt.Errorf("evm sender nonce: %w", err)
	}
	return tx.Nonce(), nil
}

// TransactionSuperseded reports a sent transaction that can never be included:
// the sender's nonce count at a block max(confirmations, minSupersededDepth)
// under the head is already past nonce (another transaction spent it) and the
// node does not know txHash. Each read that fails is an error: the caller keeps
// the transaction pending.
func (a *EVMLive) TransactionSuperseded(ctx context.Context, txHash, from string, nonce uint64) (bool, error) {
	if strings.TrimSpace(txHash) == "" || strings.TrimSpace(from) == "" {
		return false, fmt.Errorf("evm superseded: tx hash and sender are required")
	}
	head, err := a.GetLatestBlock(ctx)
	if err != nil {
		return false, err
	}
	depth := max(a.cfg.Confirmations, minSupersededDepth)
	if head < depth {
		return false, nil
	}
	var count string
	if err := a.rpc.Call(ctx, "eth_getTransactionCount", &count, from, fmt.Sprintf("0x%x", head-depth)); err != nil {
		return false, err
	}
	spent, err := parseHexQuantity(count, "nonce count")
	if err != nil {
		return false, err
	}
	if !spent.IsUint64() || spent.Uint64() <= nonce {
		return false, nil
	}
	var known json.RawMessage
	if err := a.rpc.Call(ctx, "eth_getTransactionByHash", &known, txHash); err != nil {
		return false, err
	}
	return len(known) == 0 || string(known) == "null", nil
}
