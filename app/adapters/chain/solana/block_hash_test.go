package solana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestSolana_BlockHashAt_ReadsTheFinalizedBlockhashWithoutTransactions(t *testing.T) {
	live, requests := serveSolanaFixture(t, "getBlock_transfers.json")

	hash, err := live.BlockHashAt(context.Background(), fixtureSlot)

	if err != nil || hash != "Braetyz9fCW1P6ahjpxpBqLVDCh61kZPSNgZwx8okPt2" {
		t.Fatalf("hash %q err %v", hash, err)
	}
	if len(*requests) != 1 || (*requests)[0].Method != "getBlock" || len((*requests)[0].Params) != 2 {
		t.Fatalf("requests %+v, want one getBlock", *requests)
	}
	var options map[string]any
	if err := json.Unmarshal((*requests)[0].Params[1], &options); err != nil {
		t.Fatal(err)
	}
	if options["transactionDetails"] != "none" || options["commitment"] != "finalized" || options["rewards"] != false {
		t.Fatalf("options %v, want the finalized block without transactions or rewards", options)
	}
}

func TestSolana_BlockHashAt_ASlotWithoutABlockIsAnError(t *testing.T) {
	live, _ := serveSolanaFixture(t, "getBlock_skipped.json")
	if hash, err := live.BlockHashAt(context.Background(), 506348904); err == nil || hash != "" {
		t.Fatalf("hash %q accepted for a skipped slot, want an error: the recorded block was finalized", hash)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	t.Cleanup(srv.Close)
	empty := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL})
	if hash, err := empty.BlockHashAt(context.Background(), 1); err == nil || hash != "" {
		t.Fatalf("hash %q accepted for a null block", hash)
	}
}
