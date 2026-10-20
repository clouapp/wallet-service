package bitcoin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// spendsEsplora is an Esplora indexer of Litecoin testnet whose tip, transaction
// statuses and outspends the test decides.
type spendsEsplora struct {
	tip       uint64
	statuses  map[string]string
	outspends map[string]string
	calls     atomic.Int64
}

func newSpendsEsplora(t *testing.T, tip uint64) (*spendsEsplora, string) {
	t.Helper()
	fake := &spendsEsplora{tip: tip, statuses: map[string]string{}, outspends: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.calls.Add(1)
		path := r.URL.Path
		switch {
		case path == "/block-height/0":
			_, _ = io.WriteString(w, ltcTestnetGenesisHash)
		case path == "/blocks/tip/height":
			_, _ = fmt.Fprintf(w, "%d", fake.tip)
		case strings.HasSuffix(path, "/status"):
			status, ok := fake.statuses[strings.TrimSuffix(strings.TrimPrefix(path, "/tx/"), "/status")]
			if !ok {
				http.Error(w, "Transaction not found", http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, status)
		case strings.Contains(path, "/outspend/"):
			answer, ok := fake.outspends[strings.TrimPrefix(path, "/tx/")]
			if !ok {
				answer = `{"spent":false}`
			}
			_, _ = io.WriteString(w, answer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return fake, server.URL
}

func spentBy(txID string, height uint64) string {
	if height == 0 {
		return `{"spent":true,"txid":"` + txID + `","vin":0,"status":{"confirmed":false}}`
	}
	return fmt.Sprintf(`{"spent":true,"txid":"%s","vin":0,"status":{"confirmed":true,"block_height":%d}}`, txID, height)
}

func outspendKey(outpoint string) string {
	txID, vout, _ := strings.Cut(outpoint, ":")
	return txID + "/outspend/" + vout
}

func newSpendsLive(t *testing.T, confirmations uint64, primaryURL string, fallbacks ...string) *BitcoinLive {
	t.Helper()
	live := newFailoverTestLive(t, primaryURL, fallbacks...)
	live.cfg.Confirmations = confirmations
	return live
}

var (
	ourTxID   = strings.Repeat("a1", 32)
	otherTxID = strings.Repeat("b2", 32)
	inputA    = strings.Repeat("c3", 32) + ":0"
	inputB    = strings.Repeat("d4", 32) + ":3"
)

func TestBitcoin_SpentInputs_AreTheOutpointsTheSignedTransactionSpends(t *testing.T) {
	raw, _ := failoverTestRawTx(t)
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainTLTC, NativeSymbol: models.NativeLTC, IsTestnet: true})

	inputs, err := live.SpentInputs(&types.SignedTx{RawBytes: raw})

	require.NoError(t, err)
	assert.Equal(t, []string{failoverTestUTXOTx + ":1"}, inputs)
	_, err = live.SpentInputs(&types.SignedTx{RawBytes: []byte{1, 2, 3}})
	assert.Error(t, err)
}

func TestBitcoin_SearchTransactionBlock_FindsTheTransactionOnAnyProvider(t *testing.T) {
	_, primaryURL := newSpendsEsplora(t, 5000)
	fallback, fallbackURL := newSpendsEsplora(t, 5000)
	fallback.statuses[ourTxID] = `{"confirmed":true,"block_height":4990}`
	live := newSpendsLive(t, 6, primaryURL, fallbackURL)

	block, err := live.GetTransactionBlock(context.Background(), ourTxID)
	require.NoError(t, err)
	assert.Zero(t, block, "the failover order stops at the primary, which does not know it")

	block, err = live.SearchTransactionBlock(context.Background(), ourTxID)
	require.NoError(t, err)
	assert.Equal(t, uint64(4990), block)

	unknown, err := live.SearchTransactionBlock(context.Background(), otherTxID)
	require.NoError(t, err)
	assert.Zero(t, unknown)
}

func TestBitcoin_InputsSpend_ProvesADoubleSpendOnlyWhenEveryInputIsSpentElsewhereAndDeep(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		spends    map[string]string
		want      chain.InputsSpend
		inputsSet []string
	}{
		{
			name:   "every input spent by another transaction six blocks deep",
			spends: map[string]string{inputA: spentBy(otherTxID, 4990), inputB: spentBy(otherTxID, 4995)},
			want:   chain.InputsSpend{Conflicted: true, Block: 4995},
		},
		{
			name:   "one input still unspent",
			spends: map[string]string{inputA: spentBy(otherTxID, 4990)},
			want:   chain.InputsSpend{},
		},
		{
			name:   "a spend not deep enough yet",
			spends: map[string]string{inputA: spentBy(otherTxID, 4990), inputB: spentBy(otherTxID, 4996)},
			want:   chain.InputsSpend{},
		},
		{
			name:   "a spend still in the mempool",
			spends: map[string]string{inputA: spentBy(otherTxID, 4990), inputB: spentBy(otherTxID, 0)},
			want:   chain.InputsSpend{},
		},
		{
			name:   "an input spent by our own transaction",
			spends: map[string]string{inputA: spentBy(ourTxID, 4999), inputB: spentBy(ourTxID, 4999)},
			want:   chain.InputsSpend{Ours: true, Block: 4999},
		},
		{
			name:   "our own transaction still in the mempool",
			spends: map[string]string{inputA: spentBy(ourTxID, 0)},
			want:   chain.InputsSpend{Ours: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			esplora, url := newSpendsEsplora(t, 5000)
			for outpoint, answer := range tc.spends {
				esplora.outspends[outspendKey(outpoint)] = answer
			}
			spend, err := newSpendsLive(t, 6, url).InputsSpend(ctx, ourTxID, []string{inputA, inputB})
			require.NoError(t, err)
			assert.Equal(t, tc.want, spend)
		})
	}
	t.Run("no input is no proof", func(t *testing.T) {
		_, url := newSpendsEsplora(t, 5000)
		_, err := newSpendsLive(t, 6, url).InputsSpend(ctx, ourTxID, nil)
		require.Error(t, err)
	})
	t.Run("a provider that cannot read spends leaves them to one that can", func(t *testing.T) {
		node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusBadGateway)
		}))
		t.Cleanup(node.Close)
		esplora, url := newSpendsEsplora(t, 5000)
		esplora.outspends[outspendKey(inputA)] = spentBy(otherTxID, 4990)
		live := newSpendsLive(t, 6, node.URL, url)
		spend, err := live.InputsSpend(ctx, ourTxID, []string{inputA})
		require.NoError(t, err)
		assert.Equal(t, chain.InputsSpend{Conflicted: true, Block: 4990}, spend)
	})
}

func TestBitcoin_InputsSpend_IsUnsupportedOnANodeWithoutASpendIndex(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a JSON-RPC node has no spend index and must not be asked")
		http.Error(w, "no", http.StatusBadRequest)
	}))
	t.Cleanup(node.Close)
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainTLTC, NativeSymbol: models.NativeLTC, RPCURL: node.URL, IsTestnet: true})

	_, err := live.InputsSpend(context.Background(), ourTxID, []string{inputA})

	require.ErrorIs(t, err, errProviderUnsupported)
}
