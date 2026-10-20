package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
)

// pooledEVMNode is a Sepolia node that has no receipt for any transaction and
// answers eth_sendRawTransaction with refusal, as a geth node does for bytes it
// still holds in its pool ("already known") or for its own reasons.
func pooledEVMNode(t *testing.T, refusal string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "eth_chainId":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0xaa36a7"}`)
		case "eth_sendRawTransaction":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"`+refusal+`"}}`)
		default:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":null}`)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestSettle_Unanswered_OnAnEVMNode_ARetryNeverPaysTwice(t *testing.T) {
	for _, tc := range []struct {
		refusal string
		outcome string
		invalid bool
	}{
		// The original still sits in the primary's pool: crash after the send, retry
		// before it is mined.
		{"already known", chain.BroadcastOutcomeAccepted, false},
		// The original landed (or another spent the nonce); a lagging endpoint may say so too.
		{"nonce too low: next nonce 42, tx nonce 41", chain.BroadcastOutcomeUnknown, false},
		{"replacement transaction underpriced", chain.BroadcastOutcomeUnknown, false},
		{"txpool is full", chain.BroadcastOutcomeUnknown, false},
		{"max fee per gas less than block base fee: address 0x1, maxFeePerGas: 1, baseFee: 9", chain.BroadcastOutcomeUnknown, false},
		{"invalid sender", "", true},
	} {
		t.Run(tc.refusal, func(t *testing.T) {
			registry := chain.NewRegistry()
			registry.RegisterChain(chainfixtures.NewEVMSepolia(pooledEVMNode(t, tc.refusal)))
			txRepo := &fakeTxRepo{}
			svc := &service{registry: registry, txRepo: txRepo, sealer: testSealer{}, sleep: func(context.Context, time.Duration) error { return nil }}
			row := unansweredRow(sealedSignedBefore)
			row.Chain = models.ChainTETH
			txRepo.created = append(txRepo.created, row)

			settled, err := svc.SettleUnanswered(context.Background(), row)

			if tc.invalid {
				if !errors.Is(err, ErrSignedBytesInvalid) || txRepo.failed[row.ID] != broadcastRefusedReason {
					t.Fatalf("err %v failed %v: bytes that can never be valid fail the row", err, txRepo.failed)
				}
				return
			}
			if err != nil || settled == nil || txRepo.failed[row.ID] != "" {
				t.Fatalf("err %v failed %v: the row is replayed as broadcast and never failed (a re-signed payment would land too)", err, txRepo.failed)
			}
			if got := payloadOf(t, row.RawPayload)[chain.BroadcastOutcomePayloadKey]; got != tc.outcome {
				t.Fatalf("outcome %v, want %s", got, tc.outcome)
			}
		})
	}
}
