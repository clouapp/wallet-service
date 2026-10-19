package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/app/services/chain"
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

// ExpiryEvidence reads, for a transfer whose blockhash's last valid block height is
// lastValidBlock, whether the finalized block height is past it (getEpochInfo, which
// gives the finalized slot and height together) and what each endpoint (the primary,
// then every other ready one, with searchTransactionHistory) says of its signature.
// An answer whose context slot is behind that finalized slot comes from a backend
// that may not have the transfer's slot yet: it is no witness. Any read that fails
// is an error.
func (a *SolanaLive) ExpiryEvidence(ctx context.Context, signature string, lastValidBlock uint64) (chain.ExpiryEvidence, error) {
	if signature == "" || lastValidBlock == 0 {
		return chain.ExpiryEvidence{}, fmt.Errorf("sol expiry: signature and last valid block height are required")
	}
	var epoch struct {
		AbsoluteSlot uint64 `json:"absoluteSlot"`
		BlockHeight  uint64 `json:"blockHeight"`
	}
	if err := a.rpc.Call(ctx, "getEpochInfo", &epoch, map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return chain.ExpiryEvidence{}, err
	}
	if epoch.BlockHeight <= lastValidBlock {
		return chain.ExpiryEvidence{}, nil
	}
	params := []interface{}{[]string{signature}, map[string]bool{"searchTransactionHistory": true}}
	var first json.RawMessage
	if err := a.rpc.Call(ctx, "getSignatureStatuses", &first, params...); err != nil {
		return chain.ExpiryEvidence{}, err
	}
	evidence := chain.ExpiryEvidence{Past: true}
	for _, raw := range append([]json.RawMessage{first}, a.rpc.CallOthers(ctx, statusesBudget, "getSignatureStatuses", params...)...) {
		var statuses struct {
			Context struct {
				Slot uint64 `json:"slot"`
			} `json:"context"`
			Value []*solanaSignatureStatus `json:"value"`
		}
		if err := json.Unmarshal(raw, &statuses); err != nil {
			return chain.ExpiryEvidence{}, fmt.Errorf("sol expiry: signature status: %w", err)
		}
		if len(statuses.Value) > 0 && statuses.Value[0] != nil {
			return chain.ExpiryEvidence{Past: true, Known: true}, nil
		}
		if statuses.Context.Slot >= epoch.AbsoluteSlot {
			evidence.Witnesses++
		}
	}
	return evidence, nil
}
