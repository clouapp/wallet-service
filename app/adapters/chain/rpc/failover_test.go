package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
)

// fakeNode is a JSON-RPC endpoint whose answer per method the test decides. It
// counts the requests it served, per method.
type fakeNode struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  map[string]int
}

func newFakeNode(t *testing.T, answer func(method string) (status int, body string)) *fakeNode {
	t.Helper()
	node := &fakeNode{calls: map[string]int{}}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		node.mu.Lock()
		node.calls[req.Method]++
		node.mu.Unlock()
		status, body := answer(req.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(node.server.Close)
	return node
}

func (n *fakeNode) URL() string { return n.server.URL }

func (n *fakeNode) count(method string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls[method]
}

func (n *fakeNode) total() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	total := 0
	for _, c := range n.calls {
		total += c
	}
	return total
}

func result(value string) (int, string) {
	return http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":` + value + `}`
}

func rpcFailure(code int, message string) (int, string) {
	return http.StatusOK, `{"jsonrpc":"2.0","id":1,"error":{"code":` + itoa(code) + `,"message":"` + message + `"}}`
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func answers(status int, body string) func(string) (int, string) {
	return func(string) (int, string) { return status, body }
}

// testClock is a clock the test moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recordingSink keeps every health record it received.
type recordingSink struct {
	mu      sync.Mutex
	records [][]chain.RPCEndpointHealth
}

func (s *recordingSink) RecordRPCHealth(_ string, endpoints []chain.RPCEndpointHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, endpoints)
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// healthOf is the health of every endpoint of client, in order.
func healthOf(client *RPCClient) []chain.RPCEndpointHealth {
	return client.endpoints.Load().snapshot()
}

func failoverClient(primary string, fallbacks ...string) (*RPCClient, *testClock, *recordedSleeps) {
	sleeps := &recordedSleeps{}
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	client := NewRPCClient(RPCClientDeps{URL: primary, Fallbacks: fallbacks, ChainID: "eth"})
	client.now = clock.Now
	client.retry.sleep = sleeps.sleep
	client.retry.jitter = func(time.Duration) time.Duration { return 0 }
	return client, clock, sleeps
}

func TestFailover_Call_UsesThePrimaryWhileItAnswers(t *testing.T) {
	primary := newFakeNode(t, answers(result(`"0x10"`)))
	fallback := newFakeNode(t, answers(result(`"0x20"`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	var block string
	for i := 0; i < 3; i++ {
		if err := client.Call(context.Background(), "eth_blockNumber", &block); err != nil {
			t.Fatalf("Call: %v", err)
		}
	}
	if block != "0x10" || primary.total() != 3 || fallback.total() != 0 {
		t.Fatalf("block=%s primary=%d fallback=%d", block, primary.total(), fallback.total())
	}
}

func TestFailover_Call_MovesOnWhenTheEndpointCannotAnswer(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"connection refused": func(t *testing.T) string { return closedPortURL(t) },
		"http 502":           func(t *testing.T) string { return newFakeNode(t, answers(http.StatusBadGateway, "bad gateway")).URL() },
		"http 404":           func(t *testing.T) string { return newFakeNode(t, answers(http.StatusNotFound, "no such path")).URL() },
		"html body": func(t *testing.T) string {
			return newFakeNode(t, answers(http.StatusOK, "<html>maintenance</html>")).URL()
		},
		"internal error": func(t *testing.T) string { return newFakeNode(t, answers(rpcFailure(-32603, "internal error"))).URL() },
		"header not found": func(t *testing.T) string {
			return newFakeNode(t, answers(rpcFailure(-32000, "header not found"))).URL()
		},
		"rate limited": func(t *testing.T) string {
			return newFakeNode(t, answers(http.StatusTooManyRequests, "slow down")).URL()
		},
		"json-rpc 429": func(t *testing.T) string { return newFakeNode(t, answers(rpcFailure(429, "Too many requests"))).URL() },
		"limit exceeded": func(t *testing.T) string {
			return newFakeNode(t, answers(rpcFailure(-32005, "daily request count exceeded"))).URL()
		},
		"method not found": func(t *testing.T) string {
			return newFakeNode(t, answers(rpcFailure(-32601, "the method eth_getBlockReceipts does not exist"))).URL()
		},
		"method not allowed": func(t *testing.T) string {
			return newFakeNode(t, answers(rpcFailure(-32000, "method not allowed on this plan"))).URL()
		},
	}
	for name, primaryURL := range cases {
		t.Run(name, func(t *testing.T) {
			fallback := newFakeNode(t, answers(result(`"0x20"`)))
			client, _, sleeps := failoverClient(primaryURL(t), fallback.URL())

			var block string
			if err := client.Call(context.Background(), "eth_blockNumber", &block); err != nil {
				t.Fatalf("Call: %v", err)
			}
			if block != "0x20" || fallback.count("eth_blockNumber") != 1 {
				t.Fatalf("block=%s fallback calls=%d", block, fallback.count("eth_blockNumber"))
			}
			if len(sleeps.delays) != 0 {
				t.Fatalf("a rate-limited endpoint with a fallback behind it must not back off: %v", sleeps.delays)
			}
		})
	}
}

func TestFailover_Call_ReturnsAnswersAboutTheRequestWithoutMovingOn(t *testing.T) {
	cases := map[string][2]any{
		"execution reverted": {3, "execution reverted"},
		"invalid params":     {-32602, "invalid argument 0: hex string without 0x prefix"},
		"nonce too low":      {-32000, "nonce too low"},
		"insufficient funds": {-32000, "insufficient funds for gas * price + value"},
		"slot skipped":       {-32007, "Slot 512 was skipped, or missing due to ledger jump to recent snapshot"},
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			primary := newFakeNode(t, answers(rpcFailure(failure[0].(int), failure[1].(string))))
			fallback := newFakeNode(t, answers(result(`"0x20"`)))
			client, _, _ := failoverClient(primary.URL(), fallback.URL())

			err := client.Call(context.Background(), "eth_call", nil)
			var rpcErr *RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != failure[0].(int) {
				t.Fatalf("err = %v", err)
			}
			if fallback.total() != 0 {
				t.Fatalf("an answer about the request moved on to the fallback")
			}
			if health := healthOf(client); health[0].ConsecutiveFailures != 0 {
				t.Fatalf("an endpoint that answered counted a failure: %+v", health[0])
			}
		})
	}
}

func TestFailover_Call_ReturnsTheLastErrorWhenEveryEndpointFails(t *testing.T) {
	primary := newFakeNode(t, answers(http.StatusServiceUnavailable, "down"))
	fallback := newFakeNode(t, answers(http.StatusBadGateway, "down too"))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	err := client.Call(context.Background(), "eth_blockNumber", nil)
	var failure *chain.Failure
	if !errors.As(err, &failure) || failure.Status != http.StatusBadGateway || !errors.Is(err, chain.ErrProviderUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if primary.total() != 1 || fallback.total() != 1 {
		t.Fatalf("primary=%d fallback=%d", primary.total(), fallback.total())
	}
}

func TestFailover_Call_KeepsTheBackoffOnTheLastEndpoint(t *testing.T) {
	primary := newFakeNode(t, answers(http.StatusTooManyRequests, "slow down"))
	var fallbackCalls atomic.Int32
	fallback := newFakeNode(t, func(string) (int, string) {
		if fallbackCalls.Add(1) <= 2 {
			return http.StatusTooManyRequests, "slow down"
		}
		return result(`"0x20"`)
	})
	client, _, sleeps := failoverClient(primary.URL(), fallback.URL())

	var block string
	if err := client.Call(context.Background(), "eth_blockNumber", &block); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if block != "0x20" || primary.total() != 1 || fallback.total() != 3 || len(sleeps.delays) != 2 {
		t.Fatalf("block=%s primary=%d fallback=%d sleeps=%v", block, primary.total(), fallback.total(), sleeps.delays)
	}
}

func TestFailover_Null_AsksTheNextEndpointBeforeBelievingIt(t *testing.T) {
	receipt := `{"blockNumber":"0xb62506","status":"0x1"}`
	primary := newFakeNode(t, answers(result(`null`)))
	second := newFakeNode(t, answers(result(`null`)))
	third := newFakeNode(t, answers(result(receipt)))
	client, _, _ := failoverClient(primary.URL(), second.URL(), third.URL())

	var got *struct {
		BlockNumber string `json:"blockNumber"`
	}
	if err := client.Call(context.Background(), "eth_getTransactionReceipt", &got, "0xabc"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got == nil || got.BlockNumber != "0xb62506" {
		t.Fatalf("receipt = %+v", got)
	}
	if primary.total() != 1 || second.total() != 1 || third.total() != 1 {
		t.Fatalf("primary=%d second=%d third=%d", primary.total(), second.total(), third.total())
	}
	if health := healthOf(client); health[0].ConsecutiveFailures != 0 || health[1].ConsecutiveFailures != 0 {
		t.Fatalf("a null answer counted as a failure: %+v", health)
	}
}

func TestFailover_Null_FromEveryEndpointIsNotFound(t *testing.T) {
	primary := newFakeNode(t, answers(result(`null`)))
	fallback := newFakeNode(t, answers(result(`null`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	got := &struct{}{}
	if err := client.Call(context.Background(), "getTransaction", &got, "sig"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != nil || primary.total() != 1 || fallback.total() != 1 {
		t.Fatalf("got=%v primary=%d fallback=%d", got, primary.total(), fallback.total())
	}
}

func TestFailover_Null_StandsWhenTheOtherEndpointsCannotAnswer(t *testing.T) {
	primary := newFakeNode(t, answers(result(`null`)))
	client, _, _ := failoverClient(primary.URL(), closedPortURL(t))

	got := &struct{}{}
	if err := client.Call(context.Background(), "eth_getBlockByNumber", &got, "0x10", false); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %v", got)
	}
}

func TestFailover_Null_OfAnotherMethodIsBelievedAtOnce(t *testing.T) {
	primary := newFakeNode(t, answers(result(`null`)))
	fallback := newFakeNode(t, answers(result(`"0x1"`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	if err := client.Call(context.Background(), "eth_getLogs", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if fallback.total() != 0 {
		t.Fatal("a method outside the null list asked the fallback")
	}
}

func TestFailover_Cooldown_SkipsAFailingEndpointAndRetriesItLater(t *testing.T) {
	var primaryDown atomic.Bool
	primaryDown.Store(true)
	primary := newFakeNode(t, func(string) (int, string) {
		if primaryDown.Load() {
			return http.StatusBadGateway, "down"
		}
		return result(`"0x10"`)
	})
	fallback := newFakeNode(t, answers(result(`"0x20"`)))
	client, clock, _ := failoverClient(primary.URL(), fallback.URL())
	call := func() string {
		t.Helper()
		var block string
		if err := client.Call(context.Background(), "eth_blockNumber", &block); err != nil {
			t.Fatalf("Call: %v", err)
		}
		return block
	}

	for i := 0; i < endpointFailuresToOpen; i++ {
		call()
	}
	if primary.total() != endpointFailuresToOpen {
		t.Fatalf("primary calls = %d", primary.total())
	}
	health := healthOf(client)[0]
	if !health.CoolingDownUntil.Equal(clock.Now().Add(endpointCooldownBase)) || health.LastError != "http 502" {
		t.Fatalf("primary health = %+v", health)
	}

	call()
	if primary.total() != endpointFailuresToOpen {
		t.Fatal("a cooling-down endpoint was called")
	}

	clock.Advance(endpointCooldownBase)
	call()
	if primary.total() != endpointFailuresToOpen+1 {
		t.Fatal("the endpoint was not retried after its cool-down")
	}
	if until := healthOf(client)[0].CoolingDownUntil; !until.Equal(clock.Now().Add(2 * endpointCooldownBase)) {
		t.Fatalf("one failure after a cool-down must skip it again for twice as long, until %s", until)
	}

	clock.Advance(2 * endpointCooldownBase)
	primaryDown.Store(false)
	if block := call(); block != "0x10" {
		t.Fatalf("block = %s", block)
	}
	recovered := healthOf(client)[0]
	if recovered.ConsecutiveFailures != 0 || !recovered.CoolingDownUntil.IsZero() {
		t.Fatalf("a success did not clear the endpoint: %+v", recovered)
	}
}

func TestFailover_Cooldown_IsCappedAtFiveMinutes(t *testing.T) {
	member := &endpoint{}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	var last time.Duration
	for i := 0; i < 40; i++ {
		if cooldown, _ := member.fail(now, "timeout"); cooldown > 0 {
			last = cooldown
			now = now.Add(cooldown)
		}
	}
	if last != endpointCooldownMax {
		t.Fatalf("cool-down = %s", last)
	}
}

func TestFailover_Cooldown_TriesEveryEndpointWhenAllAreCoolingDown(t *testing.T) {
	primary := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	fallback := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	for i := 0; i < endpointFailuresToOpen+2; i++ {
		_ = client.Call(context.Background(), "eth_blockNumber", nil)
	}
	if primary.total() != endpointFailuresToOpen+2 || fallback.total() != endpointFailuresToOpen+2 {
		t.Fatalf("primary=%d fallback=%d", primary.total(), fallback.total())
	}
}

func TestFailover_Unsupported_MovesOnWithoutCountingAFailure(t *testing.T) {
	primary := newFakeNode(t, func(method string) (int, string) {
		if method == "eth_getBlockReceipts" {
			return rpcFailure(-32601, "method not found")
		}
		return result(`"0x10"`)
	})
	fallback := newFakeNode(t, answers(result(`[]`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	for i := 0; i < endpointFailuresToOpen+1; i++ {
		if err := client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1"); err != nil {
			t.Fatalf("Call: %v", err)
		}
	}
	if health := healthOf(client)[0]; health.ConsecutiveFailures != 0 || !health.CoolingDownUntil.IsZero() {
		t.Fatalf("an unsupported method put the endpoint in a cool-down: %+v", health)
	}
	if err := client.Call(context.Background(), "eth_blockNumber", nil); err != nil || primary.count("eth_blockNumber") != 1 {
		t.Fatalf("the primary no longer serves the methods it supports: err=%v", err)
	}
}

func TestFailover_Unsupported_EverywhereKeepsTheJSONRPCError(t *testing.T) {
	primary := newFakeNode(t, answers(rpcFailure(-32601, "method not found")))
	fallback := newFakeNode(t, answers(rpcFailure(-32601, "method not found")))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	err := client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1")
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("err = %v", err)
	}
}

func TestFailover_Broadcast_MovesOnWhenTheConnectionWasRefused(t *testing.T) {
	fallback := newFakeNode(t, answers(result(`"0xhash"`)))
	client, _, _ := failoverClient(closedPortURL(t), fallback.URL())

	var hash string
	if err := client.Call(context.Background(), "eth_sendRawTransaction", &hash, "0xraw"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if hash != "0xhash" || fallback.count("eth_sendRawTransaction") != 1 {
		t.Fatalf("hash=%s fallback=%d", hash, fallback.count("eth_sendRawTransaction"))
	}
}

func TestFailover_Broadcast_MovesOnWhenTheEndpointRefusedBeforeReadingIt(t *testing.T) {
	for name, refusal := range map[string]func(string) (int, string){
		"rate limited":      answers(http.StatusTooManyRequests, "slow down"),
		"method not served": answers(rpcFailure(-32601, "method not found")),
	} {
		t.Run(name, func(t *testing.T) {
			primary := newFakeNode(t, refusal)
			fallback := newFakeNode(t, answers(result(`"0xhash"`)))
			client, _, _ := failoverClient(primary.URL(), fallback.URL())

			if err := client.Call(context.Background(), "sendTransaction", nil, "raw"); err != nil {
				t.Fatalf("Call: %v", err)
			}
			if fallback.count("sendTransaction") != 1 {
				t.Fatal("the fallback did not receive the broadcast")
			}
		})
	}
}

func TestFailover_Broadcast_StopsWhenTheEndpointMayHaveAcceptedIt(t *testing.T) {
	cases := map[string]func(string) (int, string){
		"http 503":       answers(http.StatusServiceUnavailable, "unavailable"),
		"internal error": answers(rpcFailure(-32603, "internal error")),
		"html body":      answers(http.StatusOK, "<html>oops</html>"),
		"rejection":      answers(rpcFailure(-32000, "replacement transaction underpriced")),
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			primary := newFakeNode(t, failure)
			fallback := newFakeNode(t, answers(result(`"0xhash"`)))
			client, _, _ := failoverClient(primary.URL(), fallback.URL())

			if err := client.Call(context.Background(), "eth_sendRawTransaction", nil, "0xraw"); err == nil {
				t.Fatal("expected the primary's error")
			}
			if fallback.total() != 0 {
				t.Fatal("a broadcast the primary may have accepted was sent to the fallback")
			}
		})
	}
}

func TestFailover_Broadcast_StopsAfterATimeout(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	fallback := newFakeNode(t, answers(result(`"0xhash"`)))
	client, _, _ := failoverClient(hanging.URL, fallback.URL())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err := client.Call(ctx, "eth_sendRawTransaction", nil, "0xraw"); err == nil {
		t.Fatal("expected a timeout")
	}
	if fallback.total() != 0 {
		t.Fatal("a broadcast that timed out after it was sent went to the fallback")
	}
}

func TestFailover_Broadcast_AlreadyKnownOnTheNextEndpointIsASuccess(t *testing.T) {
	for _, message := range []string{"already known", "Transaction simulation failed: This transaction has already been processed"} {
		fallback := newFakeNode(t, answers(rpcFailure(-32000, message)))
		client, _, _ := failoverClient(closedPortURL(t), fallback.URL())

		err := client.Call(context.Background(), "eth_sendRawTransaction", nil, "0xraw")
		if !errors.Is(err, ErrBroadcastAlreadyKnown) {
			t.Fatalf("%q: err = %v", message, err)
		}
	}
}

func TestFailover_Broadcast_AlreadyKnownOnTheFirstEndpointStaysItsAnswer(t *testing.T) {
	primary := newFakeNode(t, answers(rpcFailure(-32000, "already known")))
	fallback := newFakeNode(t, answers(result(`"0xhash"`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	err := client.Call(context.Background(), "eth_sendRawTransaction", nil, "0xraw")
	var rpcErr *RPCError
	if errors.Is(err, ErrBroadcastAlreadyKnown) || !errors.As(err, &rpcErr) || fallback.total() != 0 {
		t.Fatalf("err = %v, fallback = %d", err, fallback.total())
	}
}

func TestFailover_Identity_KeepsAFallbackOnAnotherNetworkOut(t *testing.T) {
	primary := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	wrong := newFakeNode(t, func(method string) (int, string) {
		if method == "eth_chainId" {
			return result(`"0x1"`)
		}
		return result(`"0x99"`)
	})
	right := newFakeNode(t, func(method string) (int, string) {
		if method == "eth_chainId" {
			return result(`"0xaa36a7"`)
		}
		return result(`"0x20"`)
	})
	sink := &recordingSink{}
	client := NewRPCClient(RPCClientDeps{
		URL: primary.URL(), Fallbacks: []string{wrong.URL(), right.URL()}, ChainID: "eth", Health: sink,
		Identity: func(ctx context.Context, caller chain.JSONRPCCaller) error {
			return chain.CheckEVMNetwork(ctx, caller, 11155111)
		},
	})

	for i := 0; i < 2; i++ {
		var block string
		if err := client.Call(context.Background(), "eth_blockNumber", &block); err != nil || block != "0x20" {
			t.Fatalf("block=%s err=%v", block, err)
		}
	}
	if wrong.count("eth_blockNumber") != 0 {
		t.Fatal("a fallback on another network served a call")
	}
	if wrong.count("eth_chainId") != 1 || right.count("eth_chainId") != 1 {
		t.Fatalf("identity asked wrong=%d right=%d, want once each", wrong.count("eth_chainId"), right.count("eth_chainId"))
	}
	if health := healthOf(client); !health[1].WrongNetwork || health[2].WrongNetwork {
		t.Fatalf("health = %+v", health)
	}
	if primary.count("eth_chainId") != 0 {
		t.Fatal("the primary was asked for its identity")
	}
}

func TestFailover_Pin_KeepsOneEndpointForTheWholeScope(t *testing.T) {
	var primaryDown atomic.Bool
	primaryDown.Store(true)
	primary := newFakeNode(t, func(string) (int, string) {
		if primaryDown.Load() {
			return http.StatusBadGateway, "down"
		}
		return result(`"0x10"`)
	})
	var fallbackDown atomic.Bool
	fallback := newFakeNode(t, func(string) (int, string) {
		if fallbackDown.Load() {
			return http.StatusBadGateway, "down"
		}
		return result(`"0x20"`)
	})
	client, _, _ := failoverClient(primary.URL(), fallback.URL())
	ctx := PinEndpoint(context.Background())

	var block string
	if err := client.Call(ctx, "eth_getBlockByNumber", &block, "0x1", true); err != nil || block != "0x20" {
		t.Fatalf("block=%s err=%v", block, err)
	}
	primaryDown.Store(false)
	if err := client.Call(ctx, "eth_getLogs", &block); err != nil || block != "0x20" {
		t.Fatalf("the second call of the scope left the endpoint that answered first: block=%s err=%v", block, err)
	}
	fallbackDown.Store(true)
	if err := client.Call(ctx, "eth_getLogs", &block); err == nil {
		t.Fatal("a failure of the pinned endpoint moved on to another one")
	}
	if primary.count("eth_getLogs") != 0 {
		t.Fatal("the pinned scope reached another endpoint")
	}
	if err := client.Call(context.Background(), "eth_getLogs", &block); err != nil || block != "0x10" {
		t.Fatalf("a call outside the scope: block=%s err=%v", block, err)
	}
}

func TestFailover_Deadline_LeavesTimeForTheNextEndpoint(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	fallback := newFakeNode(t, answers(result(`"0x20"`)))
	client, _, _ := failoverClient(hanging.URL, fallback.URL())
	ctx, cancel := context.WithTimeout(context.Background(), failoverMinAttempt+time.Second)
	defer cancel()

	var block string
	if err := client.Call(ctx, "eth_blockNumber", &block); err != nil || block != "0x20" {
		t.Fatalf("block=%s err=%v", block, err)
	}
	if label := healthOf(client)[0].LastError; label != "timeout" {
		t.Fatalf("primary last error = %q", label)
	}
}

func TestFailover_Errors_NeverCarryAFallbackURL(t *testing.T) {
	const secret = "fallback-secret-key"
	var echo string
	fallback := newFakeNode(t, func(string) (int, string) { return http.StatusBadGateway, "down " + echo })
	echo = fallback.URL() + "/v2/" + secret
	client, _, _ := failoverClient(closedPortURL(t)+"/v2/primary-secret", echo)

	err := client.Call(context.Background(), "eth_blockNumber", nil)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(chain.CauseText(err), secret) {
		t.Fatalf("err = %v (cause %q)", err, chain.CauseText(err))
	}
}

func TestFailover_Health_IsReportedOnlyWhenItChanges(t *testing.T) {
	var primaryDown atomic.Bool
	primaryDown.Store(true)
	primary := newFakeNode(t, func(string) (int, string) {
		if primaryDown.Load() {
			return http.StatusBadGateway, "down"
		}
		return result(`"0x10"`)
	})
	fallback := newFakeNode(t, answers(result(`"0x20"`)))
	sink := &recordingSink{}
	client := NewRPCClient(RPCClientDeps{URL: primary.URL(), Fallbacks: []string{fallback.URL()}, ChainID: "eth", Health: sink})
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	client.now = clock.Now

	for i := 0; i < endpointFailuresToOpen; i++ {
		_ = client.Call(context.Background(), "eth_blockNumber", nil)
	}
	if sink.count() != 2 {
		t.Fatalf("records = %d, want 2 (first failure, cool-down)", sink.count())
	}
	_ = client.Call(context.Background(), "eth_blockNumber", nil)
	if sink.count() != 2 {
		t.Fatalf("a call served by the fallback during the cool-down was recorded: %d", sink.count())
	}
	clock.Advance(endpointCooldownBase)
	primaryDown.Store(false)
	_ = client.Call(context.Background(), "eth_blockNumber", nil)
	if sink.count() != 3 {
		t.Fatalf("records = %d, want the recovery as the third", sink.count())
	}
	last := sink.records[2][0]
	if last.Position != 1 || last.Host == "" || last.Fingerprint == "" || last.ConsecutiveFailures != 0 || last.LastError != "http 502" {
		t.Fatalf("recovery record = %+v", last)
	}
}

func TestReplace_Endpoints_KeepsTheHealthOfAnEndpointThatStays(t *testing.T) {
	fallback := newFakeNode(t, answers(result(`"0x20"`)))
	primary := closedPortURL(t)
	client, _, _ := failoverClient(primary, fallback.URL())
	_ = client.Call(context.Background(), "eth_blockNumber", nil)

	client.ReplaceEndpoints(fallback.URL(), []string{primary, primary, " "})
	health := healthOf(client)
	if len(health) != 2 || health[0].Host != chain.RPCEndpointHost(fallback.URL()) || health[1].ConsecutiveFailures != 1 {
		t.Fatalf("health = %+v", health)
	}
	if client.Endpoint() != fallback.URL() || len(client.Fallbacks()) != 1 {
		t.Fatalf("endpoint=%q fallbacks=%d", client.Endpoint(), len(client.Fallbacks()))
	}

	replacement := closedPortURL(t)
	client.ReplaceEndpoint(replacement)
	if client.Endpoint() != replacement || len(client.Fallbacks()) != 1 || client.Fallbacks()[0] != primary {
		t.Fatalf("ReplaceEndpoint must keep the fallbacks")
	}
}

func TestClassify_RPCError_MovesOnOnlyForTheNamedFailures(t *testing.T) {
	cases := []struct {
		code    int
		message string
		want    outcome
	}{
		{429, "Too many requests", outcomeRateLimited},
		{-32005, "limit exceeded", outcomeRateLimited},
		{-32000, "Your app has exceeded its compute units per second capacity, rate limit", outcomeRateLimited},
		{-32601, "Method not found", outcomeUnsupported},
		{-32004, "Block not available for slot 512", outcomeUnsupported},
		{-32000, "method not allowed", outcomeUnsupported},
		{-32603, "Internal error", outcomeUnavailable},
		{-32000, "header not found", outcomeUnavailable},
		{-32014, "Block status not yet available for slot 1", outcomeUnavailable},
		{-32016, "Minimum context slot has not been reached", outcomeUnavailable},
		{3, "execution reverted", outcomeAnswer},
		{-32602, "invalid params", outcomeAnswer},
		{-32000, "nonce too low", outcomeAnswer},
		{-32000, "already known", outcomeAnswer},
		{-32002, "Transaction simulation failed: Blockhash not found", outcomeAnswer},
		{-32007, "Slot 1 was skipped", outcomeAnswer},
		{-32015, "Transaction version (1) is not supported", outcomeAnswer},
	}
	for _, tc := range cases {
		if got := classifyRPCError(&rpcError{Code: tc.code, Message: tc.message}); got != tc.want {
			t.Errorf("%d %q: got %d, want %d", tc.code, tc.message, got, tc.want)
		}
	}
}

func TestClassify_HTTPFailure_ReadsAJSONRPCErrorBody(t *testing.T) {
	if got := classifyHTTPFailure(http.StatusBadRequest, []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid params"}}`)); got != outcomeAnswer {
		t.Fatalf("400 with invalid params = %d", got)
	}
	if got := classifyHTTPFailure(http.StatusBadRequest, []byte(`bad request`)); got != outcomeUnavailable {
		t.Fatalf("400 without a JSON-RPC error = %d", got)
	}
	if got := classifyHTTPFailure(http.StatusGatewayTimeout, []byte(`{"error":{"code":-32602,"message":"invalid params"}}`)); got != outcomeUnavailable {
		t.Fatalf("504 = %d", got)
	}
}

func TestFailover_Identity_AFallbackPromotedToPrimaryIsUsed(t *testing.T) {
	primary := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	promoted := newFakeNode(t, func(method string) (int, string) {
		if method == "eth_chainId" {
			return result(`"0x1"`)
		}
		return result(`"0x99"`)
	})
	client := NewRPCClient(RPCClientDeps{
		URL: primary.URL(), Fallbacks: []string{promoted.URL()}, ChainID: "eth",
		Identity: func(ctx context.Context, caller chain.JSONRPCCaller) error {
			return chain.CheckEVMNetwork(ctx, caller, 11155111)
		},
	})
	_ = client.Call(context.Background(), "eth_blockNumber", nil)
	require.True(t, healthOf(client)[1].WrongNetwork, "the fallback was first seen on another network")

	client.ReplaceEndpoints(promoted.URL(), nil)
	var block string
	err := client.Call(context.Background(), "eth_blockNumber", &block)
	require.NoError(t, err, "the primary, checked when it was written, is always called")
	assert.Equal(t, "0x99", block)
	assert.False(t, healthOf(client)[0].WrongNetwork)
}

func TestClassify_RPCError_AContractRevertIsAnAnswerWhateverItsReason(t *testing.T) {
	for _, tc := range []struct {
		code    int
		message string
	}{
		{3, "execution reverted: rate limit exceeded"},
		{3, "execution reverted: Timeout"},
		{3, "execution reverted: quota reached"},
		{3, "execution reverted"},
		{-32000, "execution reverted: backend paused"},
		{-32603, "execution reverted: internal error in the pool"},
		{-32015, "upstream: transaction version not supported"},
		{-32010, "timeout waiting for the index"},
	} {
		if got := classifyRPCError(&rpcError{Code: tc.code, Message: tc.message}); got != outcomeAnswer {
			t.Errorf("%d %q: got %d, want an answer", tc.code, tc.message, got)
		}
	}
}

func TestClassify_HTTPFailure_ReadsPhrasesOfAnyCodeOutsideA2xx(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32010,"message":"upstream timeout"}}`)
	if got := classifyHTTPFailure(http.StatusBadRequest, body); got != outcomeUnavailable {
		t.Fatalf("400 with an upstream timeout = %d", got)
	}
	revert := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted: rate limit exceeded"}}`)
	if got := classifyHTTPFailure(http.StatusBadRequest, revert); got != outcomeAnswer {
		t.Fatalf("400 with a revert = %d", got)
	}
}

func TestFailover_Call_AContractRevertNeverMovesOnNorCountsAFailure(t *testing.T) {
	primary := newFakeNode(t, answers(rpcFailure(3, "execution reverted: rate limit exceeded")))
	fallback := newFakeNode(t, answers(result(`"0x"`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	for i := 0; i < endpointFailuresToOpen; i++ {
		err := client.Call(context.Background(), "eth_call", nil, map[string]string{}, "latest")
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, 3, rpcErr.Code)
	}
	assert.Zero(t, fallback.total(), "a revert reason is the contract's answer, not the node's state")
	health := healthOf(client)[0]
	assert.Zero(t, health.ConsecutiveFailures)
	assert.True(t, health.CoolingDownUntil.IsZero())
}
