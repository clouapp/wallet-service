package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/pkg/types"
)

// blockhashMemory bounds the recent blockhashes whose last valid block height a
// process keeps: past it the memory starts over (a transfer built before that is
// then recorded without one, as before).
const blockhashMemory = 256

// statusesBudget bounds the question to the other endpoints about a signature.
const statusesBudget = 5 * time.Second

// blockhashHeights is the lastValidBlockHeight getLatestBlockhash answered for each
// blockhash this process built a transfer on.
type blockhashHeights struct {
	mu      sync.Mutex
	heights map[string]uint64
}

func newBlockhashHeights() *blockhashHeights {
	return &blockhashHeights{heights: map[string]uint64{}}
}

func (b *blockhashHeights) remember(blockhash string, height uint64) {
	if b == nil || blockhash == "" || height == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, known := b.heights[blockhash]; !known && len(b.heights) >= blockhashMemory {
		b.heights = map[string]uint64{}
	}
	b.heights[blockhash] = height
}

func (b *blockhashHeights) height(blockhash string) uint64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.heights[blockhash]
}

// LastValidBlock is the last block height at which the signed transaction can be
// processed: the lastValidBlockHeight getLatestBlockhash gave its recent blockhash
// when this process built it. 0 when the blockhash is not one this process fetched
// (nothing is recorded); an error when the bytes are not a transaction.
func (a *SolanaLive) LastValidBlock(signed *types.SignedTx) (uint64, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return 0, fmt.Errorf("sol last valid block: signed transaction is required")
	}
	tx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(signed.RawBytes))
	if err != nil {
		return 0, fmt.Errorf("sol last valid block: %w", err)
	}
	return a.blockhashes.height(tx.Message.RecentBlockhash.String()), nil
}

// TransactionExpired reports a transfer that can never be processed: the chain's
// finalized block height is past lastValidBlock (its blockhash expired) and no
// endpoint, asked with searchTransactionHistory, knows its signature. Any read that
// fails is an error: the transfer stays pending.
func (a *SolanaLive) TransactionExpired(ctx context.Context, signature string, lastValidBlock uint64) (bool, error) {
	if signature == "" || lastValidBlock == 0 {
		return false, fmt.Errorf("sol expiry: signature and last valid block height are required")
	}
	var height uint64
	if err := a.rpc.Call(ctx, "getBlockHeight", &height, map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return false, err
	}
	if height <= lastValidBlock {
		return false, nil
	}
	params := []interface{}{[]string{signature}, map[string]bool{"searchTransactionHistory": true}}
	var first json.RawMessage
	if err := a.rpc.Call(ctx, "getSignatureStatuses", &first, params...); err != nil {
		return false, err
	}
	answers := append([]json.RawMessage{first}, a.rpc.CallOthers(ctx, statusesBudget, "getSignatureStatuses", params...)...)
	for _, raw := range answers {
		var statuses struct {
			Value []*solanaSignatureStatus `json:"value"`
		}
		if err := json.Unmarshal(raw, &statuses); err != nil {
			return false, fmt.Errorf("sol expiry: signature status: %w", err)
		}
		if len(statuses.Value) > 0 && statuses.Value[0] != nil {
			return false, nil
		}
	}
	return true, nil
}
