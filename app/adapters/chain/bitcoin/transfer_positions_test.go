package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// positionsTxID pays an OP_RETURN at vout 0, two addresses at 1 and 2 and the first
// address again at 3: each paid output is its own transfer, at its vout.
var positionsTxID = fmt.Sprintf("%064x", 0xb47)

func outputPositions(transfers []types.DetectedTransfer) []int {
	positions := make([]int, 0, len(transfers))
	for _, transfer := range transfers {
		positions = append(positions, transfer.LogIndex)
	}
	return positions
}

func TestScan_BlockREST_GivesEachOutputItsVout(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(fmt.Sprintf("%s/block-height/%d", esploraTestPrefix, esploraTestHeight), esploraTestBlockHash)
	esplora.ok(blockPath(""), fmt.Sprintf(`{"id":"%s","height":%d,"timestamp":1790907938,"tx_count":1}`, esploraTestBlockHash, esploraTestHeight))
	esplora.ok(blockPath("/txs/0"), `[{"txid":"`+positionsTxID+`","vout":[`+
		`{"scriptpubkey_type":"op_return","value":0},`+
		`{"scriptpubkey_address":"tb1qalice","value":50000},`+
		`{"scriptpubkey_address":"tb1qbob","value":70000},`+
		`{"scriptpubkey_address":"tb1qalice","value":50000}]}]`)

	transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)
	require.NoError(t, err)

	require.Len(t, transfers, 3)
	assert.Equal(t, []int{1, 2, 3}, outputPositions(transfers))
	assert.Equal(t, "tb1qalice", transfers[0].To)
	assert.Equal(t, "tb1qbob", transfers[1].To)
	assert.Equal(t, "tb1qalice", transfers[2].To)
}

func TestScan_BlockRPC_GivesEachOutputItsVout(t *testing.T) {
	const blockHash = "00000000000000000000000000000000000000000000000000000000000b10c4"
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		result := `null`
		switch req.Method {
		case "getblockhash":
			result = `"` + blockHash + `"`
		case "getblock":
			result = `{"time":1790907938,"tx":[{"txid":"` + positionsTxID + `","vout":[` +
				`{"value":0,"n":0,"scriptPubKey":{"type":"nulldata"}},` +
				`{"value":0.0005,"n":1,"scriptPubKey":{"address":"bc1qalice"}},` +
				`{"value":0.0007,"n":2,"scriptPubKey":{"addresses":["bc1qbob"]}},` +
				`{"value":0.0005,"n":3,"scriptPubKey":{"address":"bc1qalice"}}]}]}`
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+result+`}`)
	}))
	t.Cleanup(node.Close)
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainBTC, NativeSymbol: models.NativeBTC, NativeDecimal: 8, RPCURL: node.URL})

	transfers, err := live.scanBlockRPC(context.Background(), 900000)
	require.NoError(t, err)

	require.Len(t, transfers, 3)
	assert.Equal(t, []int{1, 2, 3}, outputPositions(transfers))
	assert.Equal(t, "bc1qbob", transfers[1].To)
	assert.Equal(t, int64(70000), transfers[1].Amount.Int64())
}
