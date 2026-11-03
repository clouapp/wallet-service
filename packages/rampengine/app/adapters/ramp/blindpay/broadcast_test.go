package blindpay_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// rpcNode is a Solana node that answers the three calls the broadcast makes.
// send and status decide each answer; every JSON-RPC method seen is recorded.
type rpcNode struct {
	mu      sync.Mutex
	methods []string
	send    func() (result string, rpcError string)
	status  func(n int) string
	polls   int
}

func (n *rpcNode) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(raw, &req)
	n.mu.Lock()
	n.methods = append(n.methods, req.Method)
	if req.Method == "getSignatureStatuses" {
		n.polls++
	}
	polls := n.polls
	n.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "sendTransaction":
		result, rpcErr := n.send()
		if rpcErr != "" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":`+rpcErr+`}`)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"`+result+`"}`)
	case "getSignatureStatuses":
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":1},"value":[`+n.status(polls)+`]}}`)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (n *rpcNode) seen() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.methods...)
}

const (
	statusConfirmed = `{"slot":1,"confirmations":null,"err":null,"confirmationStatus":"confirmed"}`
	statusFinalized = `{"slot":1,"confirmations":null,"err":null,"confirmationStatus":"finalized"}`
	statusProcessed = `{"slot":1,"confirmations":1,"err":null,"confirmationStatus":"processed"}`
	statusFailed    = `{"slot":1,"confirmations":null,"err":{"InstructionError":[0,"Custom"]},"confirmationStatus":"confirmed"}`
)

// signedDelegation is a real, signed legacy transaction: the broadcast reads
// its id from the first signature.
func signedDelegation(t *testing.T) string {
	t.Helper()
	owner := solana.NewWallet()
	instruction := system.NewTransferInstruction(1, owner.PublicKey(), owner.PublicKey()).Build()
	tx, err := solana.NewTransaction([]solana.Instruction{instruction}, solana.Hash{1}, solana.TransactionPayer(owner.PublicKey()))
	require.NoError(t, err)
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(owner.PublicKey()) {
			return &owner.PrivateKey
		}
		return nil
	})
	require.NoError(t, err)
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

// broadcastAdapter is a BlindPay that creates the payout and confirms it, with
// a node behind the client; it reports whether the payout POST arrived.
func broadcastAdapter(t *testing.T, node *rpcNode) (*blindpay.Adapter, func() bool) {
	t.Helper()
	rpc := httptest.NewServer(http.HandlerFunc(node.handler))
	t.Cleanup(rpc.Close)

	created, err := os.ReadFile("testdata/payout_create_sandbox.json")
	require.NoError(t, err)
	read := payoutBody(t, nil)
	var mu sync.Mutex
	posted := false
	bp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			mu.Lock()
			posted = true
			mu.Unlock()
			_, _ = w.Write(created)
			return
		}
		_, _ = io.WriteString(w, read)
	}))
	t.Cleanup(bp.Close)

	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: models.ProviderModeSandbox,
		BaseURL: bp.URL + "/v1", Timeout: 2 * time.Second, SolanaRPCURL: rpc.URL,
	})
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)
	return adapter, func() bool { mu.Lock(); defer mu.Unlock(); return posted }
}

func finalizeWith(tx string) ramp.FinalizeRequest {
	req := finalizeRequest()
	req.SignedTransaction = tx
	return req
}

func TestFinalizeOfframp_SendsTheDelegationBeforeCreatingThePayout(t *testing.T) {
	node := &rpcNode{
		send:   func() (string, string) { return "sig", "" },
		status: func(n int) string { return map[bool]string{true: "null", false: statusConfirmed}[n == 1] },
	}
	adapter, posted := broadcastAdapter(t, node)

	order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	require.NoError(t, err)
	assert.Equal(t, payoutID, order.ProviderOrderID)
	assert.True(t, posted())
	assert.Equal(t, []string{"getSignatureStatuses", "sendTransaction", "getSignatureStatuses"}, node.seen(),
		"the delegation is looked up, sent and confirmed, in this order")
}

func TestFinalizeOfframp_ADelegationAlreadyOnChainIsNotSentAgain(t *testing.T) {
	node := &rpcNode{
		send:   func() (string, string) { t.Fatal("a landed delegation is not sent again"); return "", "" },
		status: func(int) string { return statusFinalized },
	}
	adapter, posted := broadcastAdapter(t, node)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	require.NoError(t, err)
	assert.True(t, posted())
	assert.Equal(t, []string{"getSignatureStatuses"}, node.seen())
}

func TestFinalizeOfframp_ANodeThatSawTheTransactionBeforeIsNotAnError(t *testing.T) {
	node := &rpcNode{
		send: func() (string, string) {
			return "", `{"code":-32002,"message":"Transaction simulation failed: This transaction has already been processed"}`
		},
		status: func(n int) string { return map[bool]string{true: "null", false: statusConfirmed}[n == 1] },
	}
	adapter, posted := broadcastAdapter(t, node)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	require.NoError(t, err)
	assert.True(t, posted())
}

func TestFinalizeOfframp_WaitsForTheNodeToConfirmTheDelegation(t *testing.T) {
	node := &rpcNode{
		send: func() (string, string) { return "sig", "" },
		status: func(n int) string {
			if n < 3 {
				return map[bool]string{true: "null", false: statusProcessed}[n == 1]
			}
			return statusConfirmed
		},
	}
	adapter, posted := broadcastAdapter(t, node)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	require.NoError(t, err)
	assert.True(t, posted())
	assert.Equal(t, 3, node.polls, "the payout is created only once the delegation is confirmed")
}

func TestFinalizeOfframp_ARefusedDelegationNeverCreatesThePayout(t *testing.T) {
	tests := []struct{ name, rpcError, code string }{
		{"stale blockhash", `{"code":-32002,"message":"Transaction simulation failed: Blockhash not found"}`, "delegation_blockhash_expired"},
		{"owner cannot pay", `{"code":-32002,"message":"Transaction simulation failed: Attempt to debit an account but found no record of a prior credit."}`, "delegation_rejected"},
		{"malformed", `{"code":-32602,"message":"invalid transaction"}`, "delegation_rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &rpcNode{
				send:   func() (string, string) { return "", tt.rpcError },
				status: func(int) string { return "null" },
			}
			adapter, posted := broadcastAdapter(t, node)

			_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

			var refused *ramp.FinalizeRefusedError
			require.ErrorAs(t, err, &refused, "a refusal is final: the order ends failed")
			assert.Equal(t, tt.code, refused.Code)
			assert.False(t, posted())
		})
	}
}

func TestFinalizeOfframp_ADelegationThatFailsOnChainIsRefused(t *testing.T) {
	node := &rpcNode{
		send:   func() (string, string) { return "sig", "" },
		status: func(n int) string { return map[bool]string{true: "null", false: statusFailed}[n == 1] },
	}
	adapter, posted := broadcastAdapter(t, node)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	var refused *ramp.FinalizeRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "delegation_failed", refused.Code)
	assert.False(t, posted())
}

func TestFinalizeOfframp_ATransactionThatCannotBeReadIsRefused(t *testing.T) {
	node := &rpcNode{send: func() (string, string) { return "sig", "" }, status: func(int) string { return "null" }}
	adapter, posted := broadcastAdapter(t, node)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedTx))

	var refused *ramp.FinalizeRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "delegation_unreadable", refused.Code)
	assert.False(t, posted())
	assert.Empty(t, node.seen(), "nothing is sent for a transaction that is not one")
}

// A node that does not answer decides nothing: the payout is not created, and
// the error is not a refusal, so the order is left unknown for the recovery
// to resend the same bytes.
func TestFinalizeOfframp_ANodeThatDoesNotAnswerIsAmbiguous(t *testing.T) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(rpc.Close)
	var posted bool
	bp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posted = true }))
	t.Cleanup(bp.Close)
	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: models.ProviderModeSandbox,
		BaseURL: bp.URL + "/v1", SolanaRPCURL: rpc.URL,
	})
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(signedDelegation(t)))

	require.Error(t, err)
	var refused *ramp.FinalizeRefusedError
	assert.NotErrorAs(t, err, &refused)
	assert.ErrorIs(t, err, ramp.ErrProviderFailed)
	assert.False(t, posted)
}

func TestFinalizeOfframp_WithoutANodeTheProviderIsLeftToSendTheDelegation(t *testing.T) {
	adapter, calls := payoutServer(t, createThenRead(createdBody(t, nil), func() (int, string) { return 200, payoutBody(t, nil) }))

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())

	require.NoError(t, err)
	assert.Equal(t, "POST", calls()[0].method)
}

func TestFinalizeOfframp_TheBroadcastErrorsCarryNoTransaction(t *testing.T) {
	node := &rpcNode{
		send:   func() (string, string) { return "", `{"code":-32002,"message":"Blockhash not found"}` },
		status: func(int) string { return "null" },
	}
	adapter, _ := broadcastAdapter(t, node)
	tx := signedDelegation(t)

	_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeWith(tx))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), tx)
}
