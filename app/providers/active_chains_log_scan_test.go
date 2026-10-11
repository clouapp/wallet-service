package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
)

// logsDownNode is an EVM node at block 16 whose blocks load and whose eth_getLogs
// always fails, as a node that rate-limits the log query does.
func logsDownNode(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		body := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "eth_blockNumber":
			body["result"] = "0x10"
		case "eth_getBlockByNumber":
			body["result"] = map[string]any{"hash": "0xb1", "timestamp": "0x1", "transactions": []any{}}
		default:
			body["error"] = map[string]any{"code": -32005, "message": "query returned more than 10000 results"}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRegister_ActiveChains_EVMBlockWhoseLogsFailGoesPending(t *testing.T) {
	node := logsDownNode(t)
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(string) (string, error) { return node.URL, nil }

	chainIDs := []string{models.ChainETH, models.ChainTETH, models.ChainPolygon, models.ChainTPolygon}
	rows := make([]models.Chain, 0, len(chainIDs))
	for _, chainID := range chainIDs {
		rows = append(rows, models.Chain{ID: chainID, Name: chainID, AdapterType: models.AdapterTypeEVM, NativeSymbol: "eth", NativeDecimals: 18, RequiredConfirmations: 1, RpcURL: "sealed"})
	}
	reg := chainpkg.NewRegistry()
	registerActiveChains(reg, rows, nil)

	for _, chainID := range chainIDs {
		t.Run(chainID, func(t *testing.T) {
			store, storeErr := pending.NewFileStore(pending.FileStoreDeps{Dir: t.TempDir()})
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			scanner := deposit.NewService(deposit.Deps{Registry: reg})
			scanner.SetPendingStore(store)
			policyErr := scanner.SetFailurePolicy(deposit.FailurePolicy{
				PendingRetryDelay:     time.Second,
				PendingRetryMaxDelay:  time.Second,
				MaxNewPendingPerCycle: 1,
			})
			if policyErr != nil {
				t.Fatal(policyErr)
			}

			_, scanErr := scanner.ScanBlock(context.Background(), chainID, 5)
			if scanErr == nil || !strings.Contains(scanErr.Error(), "saved as pending") {
				t.Fatalf("a block whose ERC-20 logs did not load must fail and be kept for retry, got %v", scanErr)
			}
			entries, listErr := store.List(context.Background(), chainID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(entries) != 1 || entries[0].Block != 5 || entries[0].ErrorClass != pending.ClassRPC {
				t.Fatalf("expected block 5 pending with an rpc error, got %+v", entries)
			}
		})
	}
}
