package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	_ chain.SenderNonceReader   = (*EVMLive)(nil)
	_ chain.NonceSpendReader    = (*EVMLive)(nil)
	_ chain.TransactionSearcher = (*EVMLive)(nil)
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

// spendNode is a chain where from's nonce was spent in block spentAt by the
// transaction spentBy (its receipt status receiptStatus, gas used gasUsed of gas
// 0x5208). It has no transaction index: every lookup by hash is null. State older
// than stateFrom is missing, as on a node without archive state.
type spendNode struct {
	mu            sync.Mutex
	head          uint64
	from          string
	nonce         uint64
	spentAt       uint64
	spentBy       string
	receiptStatus string
	gasUsed       string
	stateFrom     uint64
	countedAt     []uint64
}

func (n *spendNode) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n.mu.Lock()
		defer n.mu.Unlock()
		answer := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
		blockParam := func(i int) uint64 {
			var tag string
			_ = json.Unmarshal(req.Params[i], &tag)
			value, _ := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
			return value
		}
		switch req.Method {
		case "eth_blockNumber":
			answer(fmt.Sprintf("0x%x", n.head))
		case "eth_getTransactionCount":
			block := blockParam(1)
			n.countedAt = append(n.countedAt, block)
			if block < n.stateFrom {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32000, "message": "missing trie node"}})
				return
			}
			count := n.nonce
			if n.spentAt != 0 && block >= n.spentAt {
				count = n.nonce + 1
			}
			answer(fmt.Sprintf("0x%x", count))
		case "eth_getBlockByNumber":
			if blockParam(0) != n.spentAt {
				answer(map[string]any{"hash": fmt.Sprintf("0x%064x", blockParam(0)), "transactions": []any{}})
				return
			}
			answer(map[string]any{"hash": fmt.Sprintf("0x%064x", n.spentAt), "transactions": []any{
				map[string]any{"hash": "0x" + strings.Repeat("11", 32), "from": "0x00000000000000000000000000000000000000cc", "nonce": "0x0", "gas": "0x5208"},
				map[string]any{"hash": n.spentBy, "from": strings.ToUpper(n.from[:2]) + n.from[2:], "nonce": fmt.Sprintf("0x%x", n.nonce), "gas": "0x5208"},
			}})
		case "eth_getBlockReceipts":
			answer([]any{map[string]any{"transactionHash": n.spentBy, "status": n.receiptStatus, "gasUsed": n.gasUsed}})
		default:
			answer(nil)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const (
	spendSender = "0x00000000000000000000000000000000000000bb"
	ourHash     = "0x00000000000000000000000000000000000000000000000000000000000000a1"
	otherHash   = "0x00000000000000000000000000000000000000000000000000000000000000b2"
)

func spendAdapter(t *testing.T, node *spendNode) *EVMLive {
	t.Helper()
	return NewEVMLive(EVMConfig{ChainIDStr: "sepolia", RPCURL: node.serve(t).URL, Confirmations: 3})
}

func TestEVM_NonceSpend_AnotherTransactionTookTheNonce(t *testing.T) {
	node := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 950, spentBy: otherHash, receiptStatus: "0x1", gasUsed: "0x5208"}
	got, err := spendAdapter(t, node).NonceSpend(context.Background(), ourHash, spendSender, 41, 900)
	require.NoError(t, err)
	assert.Equal(t, chain.NonceSpend{Spent: true, Block: 950}, got, "positive proof: the nonce-41 transaction of block 950 is another one")
}

func TestEVM_NonceSpend_OurTransactionIsFoundWithoutATransactionIndex(t *testing.T) {
	for _, tc := range []struct {
		name, status, gasUsed, result string
	}{
		{"succeeded", "0x1", "0x5208", ""},
		{"reverted", "0x0", "0x5000", "reverted"},
		{"out of gas", "0x0", "0x5208", "out_of_gas"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 950, spentBy: ourHash, receiptStatus: tc.status, gasUsed: tc.gasUsed}
			got, err := spendAdapter(t, node).NonceSpend(context.Background(), ourHash, spendSender, 41, 900)
			require.NoError(t, err)
			assert.Equal(t, chain.NonceSpend{Spent: true, Ours: true, Block: 950, Result: tc.result}, got,
				"a node without the index answers null by hash, yet the transfer landed in block 950")
		})
	}
}

func TestEVM_NonceSpend_ProvesNothingWhileTheNonceIsFreeOrTooRecent(t *testing.T) {
	free := &spendNode{head: 1000, from: spendSender, nonce: 41}
	got, err := spendAdapter(t, free).NonceSpend(context.Background(), ourHash, spendSender, 41, 900)
	require.NoError(t, err)
	assert.Equal(t, chain.NonceSpend{}, got)

	recent := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 995, spentBy: otherHash, receiptStatus: "0x1", gasUsed: "0x5208"}
	got, err = spendAdapter(t, recent).NonceSpend(context.Background(), ourHash, spendSender, 41, 900)
	require.NoError(t, err)
	assert.Equal(t, chain.NonceSpend{}, got, "a spend less than 12 blocks deep is not settled yet")
}

func TestEVM_NonceSpend_ReadsTheNewestBlocksFirst(t *testing.T) {
	// The transfer was sent at block 100; the node keeps state from block 872 only.
	node := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 980, spentBy: otherHash, receiptStatus: "0x1", gasUsed: "0x5208", stateFrom: 872}
	got, err := spendAdapter(t, node).NonceSpend(context.Background(), ourHash, spendSender, 41, 100)
	require.NoError(t, err, "a recent spend is found from the blocks a node without archive state still has")
	assert.Equal(t, chain.NonceSpend{Spent: true, Block: 980}, got)
	for _, block := range node.countedAt {
		assert.GreaterOrEqual(t, block, uint64(872))
	}
}

func TestEVM_NonceSpend_AReadItCannotMakeProvesNothing(t *testing.T) {
	archiveNeeded := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 150, spentBy: otherHash, receiptStatus: "0x1", gasUsed: "0x5208", stateFrom: 872}
	_, err := spendAdapter(t, archiveNeeded).NonceSpend(context.Background(), ourHash, spendSender, 41, 100)
	require.Error(t, err, "without the state of the blocks around the spend nothing is proven")
	require.ErrorIs(t, err, chain.ErrHistoricalStateUnavailable, "the tracker backs off a row it cannot prove instead of asking every pass")

	before := &spendNode{head: 1000, from: spendSender, nonce: 41, spentAt: 90, spentBy: otherHash, receiptStatus: "0x1", gasUsed: "0x5208"}
	_, err = spendAdapter(t, before).NonceSpend(context.Background(), ourHash, spendSender, 41, 100)
	require.Error(t, err, "a nonce spent before the transfer was sent is not this transfer's spend")

	_, err = spendAdapter(t, before).NonceSpend(context.Background(), "", spendSender, 41, 100)
	require.Error(t, err)
}

func TestEVM_SearchTransactionBlock_AsksEveryEndpointForTheReceipt(t *testing.T) {
	const minedReceipt = `"result":{"transactionHash":"` + ourHash + `","blockNumber":"0x10","status":"0x1","gasUsed":"0x5208"}`
	primary := newCountingNode(t, map[string]string{"eth_getTransactionReceipt": `"result":null`})
	lagging := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_getTransactionReceipt": `"result":null`})
	indexed := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_getTransactionReceipt": minedReceipt})
	adapter := sepoliaWithFallbacks(primary.server.URL, lagging.server.URL, indexed.server.URL)

	block, err := adapter.SearchTransactionBlock(context.Background(), ourHash)
	require.NoError(t, err)
	assert.Equal(t, uint64(16), block, "the one endpoint with the transaction index answers")

	none := sepoliaWithFallbacks(primary.server.URL, lagging.server.URL)
	block, err = none.SearchTransactionBlock(context.Background(), ourHash)
	require.NoError(t, err)
	assert.Zero(t, block)

	const failedReceipt = `"result":{"transactionHash":"` + ourHash + `","blockNumber":"0x10","status":"0x0","gasUsed":"0x5208"}`
	reverted := newCountingNode(t, map[string]string{
		"eth_chainId": sepoliaChainIDResult, "eth_getTransactionReceipt": failedReceipt,
		"eth_getTransactionByHash": `"result":{"hash":"` + ourHash + `","gas":"0x5208"}`,
	})
	_, err = sepoliaWithFallbacks(primary.server.URL, lagging.server.URL, reverted.server.URL).SearchTransactionBlock(context.Background(), ourHash)
	var failed *chain.TransactionFailedError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, uint64(16), failed.Block)
}
