package evm

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	_ chain.SenderNonceReader            = (*EVMLive)(nil)
	_ chain.TransactionSupersededChecker = (*EVMLive)(nil)
)

func signedTransferWithNonce(t *testing.T, nonce uint64) *types.SignedTx {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	signer := gethtypes.LatestSignerForChainID(big.NewInt(11155111))
	tx, err := gethtypes.SignNewTx(key, signer, &gethtypes.DynamicFeeTx{
		ChainID: big.NewInt(11155111), Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 21000, To: &to, Value: big.NewInt(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return &types.SignedTx{RawBytes: raw, TxHash: tx.Hash().Hex()}
}

func TestEVM_SenderNonce_IsTheNonceTheSignedBytesSpend(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "sepolia"})
	got, err := adapter.SenderNonce(signedTransferWithNonce(t, 41))
	if err != nil || got != 41 {
		t.Fatalf("SenderNonce = %d, %v; want 41", got, err)
	}
	for _, signed := range []*types.SignedTx{nil, {}, {RawBytes: []byte{0x02, 0x01}}} {
		if _, err := adapter.SenderNonce(signed); err == nil {
			t.Fatalf("SenderNonce(%v) read a nonce from bytes that are not a transaction", signed)
		}
	}
}

// supersededNode answers the reads TransactionSuperseded makes and records the
// block it was asked the sender's nonce at.
type supersededNode struct {
	mu        sync.Mutex
	head      string
	count     string
	byHash    any
	countedAt []string
	methods   []string
}

func (n *supersededNode) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n.mu.Lock()
		n.methods = append(n.methods, req.Method)
		var result any
		switch req.Method {
		case "eth_blockNumber":
			result = n.head
		case "eth_getTransactionCount":
			if len(req.Params) == 2 {
				block, _ := req.Params[1].(string)
				n.countedAt = append(n.countedAt, block)
			}
			result = n.count
		case "eth_getTransactionByHash":
			result = n.byHash
		}
		n.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEVM_TransactionSuperseded_WhenNoNodeKnowsItAndADeepBlockSpentItsNonce(t *testing.T) {
	node := &supersededNode{head: "0x64", count: "0x2a"}
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "sepolia", RPCURL: node.serve(t).URL, Confirmations: 3})

	got, err := adapter.TransactionSuperseded(context.Background(), "0xabc", "0x00000000000000000000000000000000000000bb", 41)
	if err != nil || !got {
		t.Fatalf("TransactionSuperseded = %v, %v; want true (nonce 41 spent at a deep block, hash unknown)", got, err)
	}
	// The nonce is read minSupersededDepth (12) blocks under the head of 100, not at
	// the head and not at the chain's 3 confirmations: a transaction that took the
	// nonce must be settled before ours is declared unable to land.
	if len(node.countedAt) != 1 || node.countedAt[0] != "0x58" {
		t.Fatalf("nonce read at %v, want [0x58] (block 88)", node.countedAt)
	}
}

func TestEVM_TransactionSuperseded_UsesTheChainConfirmationsWhenDeeper(t *testing.T) {
	node := &supersededNode{head: "0x64", count: "0x2a"}
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: node.serve(t).URL, Confirmations: 20})

	if _, err := adapter.TransactionSuperseded(context.Background(), "0xabc", "0xbb", 41); err != nil {
		t.Fatal(err)
	}
	if len(node.countedAt) != 1 || node.countedAt[0] != "0x50" {
		t.Fatalf("nonce read at %v, want [0x50] (block 80)", node.countedAt)
	}
}

func TestEVM_TransactionSuperseded_IsFalseWhileTheTransactionCanStillLand(t *testing.T) {
	cases := []struct {
		name string
		node *supersededNode
	}{
		{"the nonce is still free at the deep block", &supersededNode{head: "0x64", count: "0x29"}},
		{"a node still knows the hash", &supersededNode{head: "0x64", count: "0x2a", byHash: map[string]any{"hash": "0xabc", "nonce": "0x29"}}},
		{"the chain is not deep enough yet", &supersededNode{head: "0x0b", count: "0x2a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := tc.node
			adapter := NewEVMLive(EVMConfig{ChainIDStr: "sepolia", RPCURL: node.serve(t).URL})
			got, err := adapter.TransactionSuperseded(context.Background(), "0xabc", "0xbb", 41)
			if err != nil || got {
				t.Fatalf("TransactionSuperseded = %v, %v; want false", got, err)
			}
		})
	}
}

func TestEVM_TransactionSuperseded_ReportsAReadItCouldNotMake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "sepolia", RPCURL: server.URL})

	got, err := adapter.TransactionSuperseded(context.Background(), "0xabc", "0xbb", 41)
	if err == nil || got {
		t.Fatalf("TransactionSuperseded = %v, %v; want an error and false", got, err)
	}
	if _, err := adapter.TransactionSuperseded(context.Background(), "", "0xbb", 41); err == nil {
		t.Fatal("TransactionSuperseded accepted an empty hash")
	}
}
