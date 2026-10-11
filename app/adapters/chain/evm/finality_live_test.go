//go:build evm_finality_live

package evm

// Read-only check against public testnet nodes (go test -tags evm_finality_live
// -run TestLive_FinalizedBlock ./app/adapters/chain/evm/): the "finalized" tag
// answers a block at most a few blocks behind the tip on Polygon Amoy and BSC
// testnet. Nothing is signed or broadcast.

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestLive_FinalizedBlockTrailsTheTip(t *testing.T) {
	for chainID, url := range map[string]string{
		models.ChainTPolygon: "https://polygon-amoy-bor-rpc.publicnode.com",
		models.ChainTBSC:     "https://bsc-testnet-rpc.publicnode.com",
	} {
		t.Run(chainID, func(t *testing.T) {
			adapter := NewEVMLive(EVMConfig{ChainIDStr: chainID, RPCURL: url})
			ctx := context.Background()
			finalized, err := adapter.FinalizedBlock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tip, err := adapter.GetLatestBlock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// A load-balanced endpoint can answer the two calls from different nodes.
			if finalized > tip+5 || tip-finalized > 64 {
				t.Fatalf("finalized %d, tip %d", finalized, tip)
			}
			t.Logf("tip %d, finalized %d", tip, finalized)
		})
	}
}
