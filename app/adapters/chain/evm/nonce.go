package evm

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	gethtypes "github.com/ethereum/go-ethereum/core/types"
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

// pendingNonce is the nonce the next transaction of from must use: the highest
// pending nonce the ready endpoints answer (a node that holds the last broadcast in
// its mempool counts it), and never below the next nonce of a transaction this
// process broadcast for from in the last nonceFloorTTL.
func (a *EVMLive) pendingNonce(ctx context.Context, from string) (uint64, error) {
	answers, err := a.rpc.CallEach(ctx, "eth_getTransactionCount", from, "pending")
	if err != nil {
		return 0, err
	}
	var highest uint64
	for _, raw := range answers {
		var hexNonce string
		if json.Unmarshal(raw, &hexNonce) != nil {
			continue
		}
		highest = max(highest, hexToUint64(hexNonce))
	}
	return max(highest, a.nonces.floor(from)), nil
}
