package evm

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	gethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
)

// nonceFloorTTL is how long a broadcast keeps the next nonce of its sender up in this
// process: long enough for the transaction to reach the other nodes' mempools, short
// enough that a transaction dropped from every mempool does not leave a gap that
// holds the sender's next transactions forever.
const nonceFloorTTL = 10 * time.Minute

// nonceFloor is the next nonce of each sender this process broadcast for. With
// fallbacks, the pending nonce can come from a node that has not seen the last
// broadcast yet and answers its nonce again (a "replacement underpriced" refusal,
// or a replacement of the earlier transaction).
type nonceFloor struct {
	mu   sync.Mutex
	next map[string]nonceAt
	now  func() time.Time
}

type nonceAt struct {
	nonce uint64
	at    time.Time
}

func newNonceFloor() *nonceFloor {
	return &nonceFloor{next: map[string]nonceAt{}, now: time.Now}
}

// raise records that raw (a signed transaction) was broadcast: its sender's next
// nonce is at least its nonce + 1. Bytes that do not decode change nothing.
func (f *nonceFloor) raise(raw []byte) {
	if f == nil {
		return
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil {
		return
	}
	key := strings.ToLower(sender.Hex())
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, ok := f.next[key]; !ok || current.nonce < tx.Nonce()+1 || f.now().Sub(current.at) > nonceFloorTTL {
		f.next[key] = nonceAt{nonce: tx.Nonce() + 1, at: f.now()}
	}
}

// floor is the next nonce of sender recorded in the last nonceFloorTTL, or 0.
func (f *nonceFloor) floor(sender string) uint64 {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.next[strings.ToLower(strings.TrimSpace(sender))]
	if !ok || f.now().Sub(entry.at) > nonceFloorTTL {
		return 0
	}
	return entry.nonce
}

const (
	// nonceOthersBudget is how long the other endpoints are given to answer the
	// pending nonce, while the primary is asked.
	nonceOthersBudget = time.Second
	// maxNonceRaise is the most the other endpoints may raise the primary's pending
	// nonce: one transfer of the sender broadcast elsewhere that the primary has not
	// seen yet. A node holding a transaction that was dropped everywhere else must
	// not leave a wider gap that holds the sender's next transactions.
	maxNonceRaise = 1
)

// pendingNonce is the nonce the next transaction of from must use. The primary's
// pending nonce decides (read with the backoff one endpoint gets; when it cannot
// answer, the failover order); the other ready endpoints, asked at the same time
// within nonceOthersBudget, may only raise it by maxNonceRaise (a node that holds
// the last broadcast in its mempool), and a divergence is logged. An endpoint that
// does not answer never fails the read. The result is never below the next nonce
// of a transaction this process broadcast for from in the last nonceFloorTTL.
func (a *EVMLive) pendingNonce(ctx context.Context, from string) (uint64, error) {
	others := make(chan []json.RawMessage, 1)
	go func() {
		others <- a.rpc.CallOthers(ctx, nonceOthersBudget, "eth_getTransactionCount", from, "pending")
	}()
	var hexNonce string
	if err := a.rpc.Call(rpc.BackOffOnRateLimits(ctx), "eth_getTransactionCount", &hexNonce, from, "pending"); err != nil {
		return 0, err
	}
	nonce := hexToUint64(hexNonce)
	lowest, highest := nonce, nonce
	for _, raw := range <-others {
		var other string
		if json.Unmarshal(raw, &other) != nil {
			continue
		}
		lowest, highest = min(lowest, hexToUint64(other)), max(highest, hexToUint64(other))
	}
	if lowest != nonce || highest != nonce {
		used := min(highest, nonce+maxNonceRaise)
		slog.Warn("evm pending nonce differs between endpoints",
			"chain", a.cfg.ChainIDStr, "from", from, "primary", nonce, "lowest", lowest, "highest", highest, "used", used)
		nonce = used
	}
	return max(nonce, a.nonces.floor(from)), nil
}
