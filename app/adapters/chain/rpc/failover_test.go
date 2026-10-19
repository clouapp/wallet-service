package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// evmChainIDReader reads eth_chainId as a decimal string, as the EVM adapter does.
func evmChainIDReader(ctx context.Context, caller chain.JSONRPCCaller) (string, error) {
	id, err := chain.EVMChainID(ctx, caller)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// fallbackList is urls as fallbacks that take their place in the list as position.
func fallbackList(urls ...string) []chain.RPCFallback {
	fallbacks := make([]chain.RPCFallback, 0, len(urls))
	for _, url := range urls {
		fallbacks = append(fallbacks, chain.RPCFallback{URL: url})
	}
	return fallbacks
}

// healthOf is the health of every endpoint of client, in order.
func healthOf(client *RPCClient) []chain.RPCEndpointHealth {
	return client.endpoints.Load().snapshot()
}

func failoverClient(primary string, fallbacks ...string) (*RPCClient, *testClock, *recordedSleeps) {
	sleeps := &recordedSleeps{}
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	client := NewRPCClient(RPCClientDeps{URL: primary, Fallbacks: fallbackList(fallbacks...), ChainID: "eth"})
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

func TestFailover_Null_AsksOneMoreEndpointPerCallInTurn(t *testing.T) {
	receipt := `{"blockNumber":"0xb62506","status":"0x1"}`
	primary := newFakeNode(t, answers(result(`null`)))
	second := newFakeNode(t, answers(result(`null`)))
	third := newFakeNode(t, answers(result(receipt)))
	client, _, _ := failoverClient(primary.URL(), second.URL(), third.URL())
	lookup := func() *struct {
		BlockNumber string `json:"blockNumber"`
	} {
		var got *struct {
			BlockNumber string `json:"blockNumber"`
		}
		require.NoError(t, client.Call(context.Background(), "eth_getTransactionReceipt", &got, "0xabc"))
		return got
	}

	assert.Nil(t, lookup(), "the primary and the second endpoint answered null")
	assert.Equal(t, []int{1, 1, 0}, []int{primary.total(), second.total(), third.total()}, "one more endpoint per call, not every one")

	got := lookup()
	require.NotNil(t, got, "the next call asks the next endpoint in turn")
	assert.Equal(t, "0xb62506", got.BlockNumber)
	assert.Equal(t, []int{2, 1, 1}, []int{primary.total(), second.total(), third.total()})
	for _, health := range healthOf(client) {
		assert.Zero(t, health.ConsecutiveFailures, "a null answer is not a failure")
	}
}

func TestFailover_Null_AChaseThatIsRateLimitedOrRefusedCountsNothing(t *testing.T) {
	for name, refusal := range map[string]func(string) (int, string){
		"rate limited":      answers(http.StatusTooManyRequests, "slow down"),
		"method not served": answers(rpcFailure(-32601, "method not found")),
	} {
		t.Run(name, func(t *testing.T) {
			primary := newFakeNode(t, answers(result(`null`)))
			keyless := newFakeNode(t, refusal)
			client, _, sleeps := failoverClient(primary.URL(), keyless.URL())

			for i := 0; i < endpointFailuresToOpen; i++ {
				got := &struct{}{}
				require.NoError(t, client.Call(context.Background(), "getTransaction", &got, "sig"))
				assert.Nil(t, got, "the primary's null stands")
			}
			assert.Zero(t, healthOf(client)[1].ConsecutiveFailures, "a second opinion the fallback declined is not its failure")
			assert.Empty(t, sleeps.delays, "the chase does not back off")
		})
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
		URL: primary.URL(), Fallbacks: fallbackList(wrong.URL(), right.URL()), ChainID: "eth", Health: sink,
		Identity: evmChainIDReader, Network: "11155111",
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
	if primary.count("eth_chainId") != 1 {
		t.Fatalf("the primary was asked its network %d times; once, then not again within %s", primary.count("eth_chainId"), primaryIdentityRetry)
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
	client := NewRPCClient(RPCClientDeps{URL: primary.URL(), Fallbacks: fallbackList(fallback.URL()), ChainID: "eth", Health: sink})
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

	client.ReplaceEndpoints(fallback.URL(), fallbackList(primary, primary, " "))
	health := healthOf(client)
	if len(health) != 2 || health[0].Host != chain.RPCEndpointHost(fallback.URL()) || health[1].ConsecutiveFailures != 1 {
		t.Fatalf("health = %+v", health)
	}
	if client.Endpoint() != fallback.URL() || len(client.Fallbacks()) != 1 {
		t.Fatalf("endpoint=%q fallbacks=%d", client.Endpoint(), len(client.Fallbacks()))
	}

	replacement := closedPortURL(t)
	client.ReplaceEndpoint(replacement)
	if client.Endpoint() != replacement || len(client.Fallbacks()) != 1 || client.Fallbacks()[0].URL != primary {
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
		URL: primary.URL(), Fallbacks: fallbackList(promoted.URL()), ChainID: "eth",
		Identity: evmChainIDReader, Network: "11155111",
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

// sepoliaIdentityClient is failoverClient with fallbacks checked for Sepolia.
func sepoliaIdentityClient(primary string, fallbacks ...string) *RPCClient {
	client, _, _ := failoverClient(primary, fallbacks...)
	client.identity, client.network = evmChainIDReader, "11155111"
	return client
}

func TestFailover_Identity_AFailedCheckNeverReplacesTheErrorOfASend(t *testing.T) {
	t.Run("broadcast refused by the primary", func(t *testing.T) {
		primary := newFakeNode(t, answers(http.StatusTooManyRequests, "slow down"))
		silent := newFakeNode(t, answers(http.StatusServiceUnavailable, "down"))
		client := sepoliaIdentityClient(primary.URL(), silent.URL())

		err := client.Call(context.Background(), "eth_sendRawTransaction", nil, "0xraw")
		require.ErrorIs(t, err, chain.ErrRateLimited, "the error is the primary's refusal, not the fallback's check")
		assert.True(t, chain.KnownFailure(chain.ClassifyBroadcast(err)), "a rate-limited broadcast stays a known failure")
		assert.Zero(t, silent.count("eth_sendRawTransaction"))
	})
	t.Run("read failed by the primary", func(t *testing.T) {
		primary := newFakeNode(t, answers(http.StatusBadGateway, "down"))
		mainnet := newFakeNode(t, answers(result(`"0x1"`)))
		client := sepoliaIdentityClient(primary.URL(), mainnet.URL())

		err := client.Call(context.Background(), "eth_blockNumber", nil)
		var failure *chain.Failure
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, http.StatusBadGateway, failure.Status)
		assert.NotErrorIs(t, err, chain.ErrWrongNetwork)
	})
	t.Run("broadcast the primary may have taken", func(t *testing.T) {
		release := make(chan struct{})
		hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(func() { close(release); hanging.Close() })
		fallback := newFakeNode(t, answers(result(`"0xaa36a7"`)))
		client := sepoliaIdentityClient(hanging.URL, fallback.URL())
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		err := client.Call(ctx, "eth_sendRawTransaction", nil, "0xraw")
		assert.ErrorIs(t, chain.ClassifyBroadcast(err), chain.ErrUnknownOutcome)
		assert.Zero(t, fallback.total(), "nothing is asked of a fallback after a send that may have been taken")
	})
}

func TestFailover_Identity_TheCallersDeadlineIsNotTheFallbacksFailure(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	client := sepoliaIdentityClient(closedPortURL(t), hanging.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_ = client.Call(ctx, "eth_blockNumber", nil)
	assert.Zero(t, healthOf(client)[1].ConsecutiveFailures, "the identity check ran out of the caller's time, not the fallback's")
}

func TestAttempt_Context_SharesOnlyTheCallersDeadline(t *testing.T) {
	ctx, cancel := attemptContext(context.Background(), 3, false)
	defer cancel()
	_, bounded := ctx.Deadline()
	assert.False(t, bounded, "without a caller deadline an attempt keeps the HTTP timeout, as before (a 64 MiB block may take long)")

	parent, cancelParent := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancelParent()
	shared, cancelShared := attemptContext(parent, 3, false)
	defer cancelShared()
	deadline, bounded := shared.Deadline()
	require.True(t, bounded)
	assert.InDelta(t, 3*time.Second, time.Until(deadline), float64(200*time.Millisecond), "a third of the caller's time for the first of three")

	last, cancelLast := attemptContext(parent, 1, false)
	defer cancelLast()
	lastDeadline, _ := last.Deadline()
	parentDeadline, _ := parent.Deadline()
	assert.Equal(t, parentDeadline, lastDeadline, "the last endpoint keeps what is left")
}

func TestFailover_Health_CarriesThePositionsOfTheStoredList(t *testing.T) {
	third := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	fifth := newFakeNode(t, answers(result(`"0x20"`)))
	client := NewRPCClient(RPCClientDeps{
		URL:       closedPortURL(t),
		Fallbacks: []chain.RPCFallback{{URL: third.URL(), Position: 3}, {URL: fifth.URL(), Position: 5}},
		ChainID:   "eth",
	})

	require.NoError(t, client.Call(context.Background(), "eth_blockNumber", nil))
	health := healthOf(client)
	assert.Equal(t, []int{1, 3, 5}, []int{health[0].Position, health[1].Position, health[2].Position},
		"positions 2 and 4 are disabled: the health keeps the positions an operator sees")

	client.ReplaceEndpoints(fifth.URL(), []chain.RPCFallback{{URL: third.URL()}})
	health = healthOf(client)
	assert.Equal(t, []int{1, 2}, []int{health[0].Position, health[1].Position}, "a fallback without a stored position takes its place in the list")
}

// chainIDNode answers eth_chainId with chainIDHex and every other call with
// otherStatus (200 answers 0x20).
func chainIDNode(t *testing.T, chainIDHex string, otherStatus int) *fakeNode {
	return newFakeNode(t, func(method string) (int, string) {
		if method == "eth_chainId" {
			return result(`"` + chainIDHex + `"`)
		}
		if otherStatus != http.StatusOK {
			return otherStatus, "down"
		}
		return result(`"0x20"`)
	})
}

func TestFailover_Identity_AFallbackMustAlsoServeThePrimarysNetwork(t *testing.T) {
	// The primary was pointed at mainnet (a profile move) while network_id still says
	// Sepolia: a Sepolia fallback must not answer for it.
	movedPrimary := chainIDNode(t, "0x1", http.StatusServiceUnavailable)
	sepolia := chainIDNode(t, "0xaa36a7", http.StatusOK)
	client := sepoliaIdentityClient(movedPrimary.URL(), sepolia.URL())

	err := client.Call(context.Background(), "eth_blockNumber", nil)
	var failure *chain.Failure
	require.ErrorAs(t, err, &failure, "the primary's failure is returned")
	assert.Equal(t, http.StatusServiceUnavailable, failure.Status)
	assert.Zero(t, sepolia.count("eth_blockNumber"), "a fallback on another network than the primary never serves")
	assert.False(t, healthOf(client)[1].WrongNetwork, "the fallback serves the chain's network; it is the primary that differs")
}

func TestFailover_Identity_WithAnUnreachablePrimaryAFallbackOnTheChainsNetworkServes(t *testing.T) {
	sepolia := chainIDNode(t, "0xaa36a7", http.StatusOK)
	client := sepoliaIdentityClient(closedPortURL(t), sepolia.URL())

	var block string
	require.NoError(t, client.Call(context.Background(), "eth_blockNumber", &block))
	assert.Equal(t, "0x20", block)
}

func TestFailover_Identity_IsAskedAgainWhenThePrimaryChanges(t *testing.T) {
	sepoliaPrimary := chainIDNode(t, "0xaa36a7", http.StatusServiceUnavailable)
	sepolia := chainIDNode(t, "0xaa36a7", http.StatusOK)
	client := sepoliaIdentityClient(sepoliaPrimary.URL(), sepolia.URL())
	require.NoError(t, client.Call(context.Background(), "eth_blockNumber", nil))
	require.Equal(t, 1, sepolia.count("eth_chainId"))

	mainnetPrimary := chainIDNode(t, "0x1", http.StatusServiceUnavailable)
	client.ReplaceEndpoint(mainnetPrimary.URL())
	before := sepolia.count("eth_blockNumber")
	_ = client.Call(context.Background(), "eth_blockNumber", nil)
	assert.Equal(t, before, sepolia.count("eth_blockNumber"), "after the primary moved, the fallback no longer serves")
}

func TestEndpoint_Identity_AWrongNetworkStaysWrong(t *testing.T) {
	member := &endpoint{}
	member.markIdentity(true)
	member.markIdentity(false)
	verified, wrong := member.identity()
	assert.False(t, verified)
	assert.True(t, wrong, "a concurrent check that passed does not clear a wrong network")
}

func TestClassify_RPCError_ReadsSolanaSlotAnswersByTheirMessage(t *testing.T) {
	cases := []struct {
		code    int
		message string
		want    outcome
	}{
		{-32007, "Slot 512 was skipped, or missing due to ledger jump to recent snapshot", outcomeAnswer},
		{-32009, "Slot 512 was skipped, or missing in long-term storage", outcomeAnswer},
		{-32007, "request limit reached", outcomeRateLimited},
		{-32009, "upstream unavailable", outcomeUnavailable},
		{-32001, "Block 512 cleaned up, does not exist on node. First available block: 900", outcomeUnsupported},
		{-32011, "Transaction history is not available from this node", outcomeUnsupported},
	}
	for _, tc := range cases {
		if got := classifyRPCError(&rpcError{Code: tc.code, Message: tc.message}); got != tc.want {
			t.Errorf("%d %q: got %d, want %d", tc.code, tc.message, got, tc.want)
		}
	}
}

const skippedSlot = "Slot 512 was skipped, or missing due to ledger jump to recent snapshot"

func TestFailover_SkippedSlot_FromTheprimaryIsBelievedAtOnce(t *testing.T) {
	primary := newFakeNode(t, answers(rpcFailure(-32007, skippedSlot)))
	fallback := newFakeNode(t, answers(result(`{"blockhash":"x"}`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	err := client.Call(context.Background(), "getBlock", nil, 512)
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, -32007, rpcErr.Code)
	assert.Zero(t, fallback.total())
}

func TestFailover_SkippedSlot_FromAFallbackMustBeConfirmed(t *testing.T) {
	t.Run("another endpoint has the block", func(t *testing.T) {
		gap := newFakeNode(t, answers(rpcFailure(-32007, skippedSlot)))
		full := newFakeNode(t, answers(result(`{"blockhash":"x"}`)))
		client, _, _ := failoverClient(closedPortURL(t), gap.URL(), full.URL())

		var block struct {
			Blockhash string `json:"blockhash"`
		}
		require.NoError(t, client.Call(context.Background(), "getBlock", &block, 512))
		assert.Equal(t, "x", block.Blockhash, "a snapshot gap on one fallback is not a skipped slot")
	})
	t.Run("another endpoint says skipped too", func(t *testing.T) {
		first := newFakeNode(t, answers(rpcFailure(-32007, skippedSlot)))
		second := newFakeNode(t, answers(rpcFailure(-32009, "Slot 512 was skipped, or missing in long-term storage")))
		client, _, _ := failoverClient(closedPortURL(t), first.URL(), second.URL())

		err := client.Call(context.Background(), "getBlock", nil, 512)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr, "two endpoints agree: the slot was skipped")
		assert.Equal(t, -32007, rpcErr.Code)
	})
	t.Run("no other endpoint to ask", func(t *testing.T) {
		gap := newFakeNode(t, answers(rpcFailure(-32007, skippedSlot)))
		client, _, _ := failoverClient(closedPortURL(t), gap.URL())

		err := client.Call(context.Background(), "getBlock", nil, 512)
		var rpcErr *RPCError
		require.Error(t, err)
		assert.False(t, errors.As(err, &rpcErr), "an unconfirmed skip is not a skip: the scan retries the slot")
		assert.ErrorIs(t, err, chain.ErrProviderUnavailable)
	})
}

func TestFailover_SolanaHistoryAnswers_MoveOnWithoutCountingAFailure(t *testing.T) {
	for _, failure := range []func(string) (int, string){
		answers(rpcFailure(-32001, "Block 512 cleaned up, does not exist on node. First available block: 900")),
		answers(rpcFailure(-32011, "Transaction history is not available from this node")),
	} {
		shortHistory := newFakeNode(t, failure)
		archive := newFakeNode(t, answers(result(`{"blockhash":"x"}`)))
		client, _, _ := failoverClient(shortHistory.URL(), archive.URL())

		require.NoError(t, client.Call(context.Background(), "getBlock", nil, 512))
		assert.Equal(t, 1, archive.total())
		assert.Zero(t, healthOf(client)[0].ConsecutiveFailures)
	}
}

func TestFailover_Null_ASecondOpinionThatHangsIsCutShort(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	primary := newFakeNode(t, answers(result(`null`)))
	client, _, _ := failoverClient(primary.URL(), hanging.URL)

	started := time.Now()
	got := &struct{}{}
	require.NoError(t, client.Call(context.Background(), "eth_getTransactionReceipt", &got, "0xabc"))
	assert.Nil(t, got)
	assert.Less(t, time.Since(started), secondOpinionTimeout+2*time.Second, "a poll without a caller deadline is not held for the HTTP timeout")
}

func TestCall_Each_AsksEveryReadyEndpointAndKeepsTheAnswers(t *testing.T) {
	primary := newFakeNode(t, answers(result(`"0x5"`)))
	ahead := newFakeNode(t, answers(result(`"0x7"`)))
	down := newFakeNode(t, answers(http.StatusBadGateway, "down"))
	client, _, _ := failoverClient(primary.URL(), ahead.URL(), down.URL())

	answersGot, err := client.CallEach(context.Background(), "eth_getTransactionCount", "0xabc", "pending")
	require.NoError(t, err)
	got := make([]string, 0, len(answersGot))
	for _, raw := range answersGot {
		got = append(got, string(raw))
	}
	assert.ElementsMatch(t, []string{`"0x5"`, `"0x7"`}, got)
	assert.Equal(t, 1, healthOf(client)[2].ConsecutiveFailures)

	nobody, _, _ := failoverClient(closedPortURL(t), down.URL())
	_, err = nobody.CallEach(context.Background(), "eth_getTransactionCount", "0xabc", "pending")
	assert.ErrorIs(t, err, chain.ErrProviderUnavailable)

	single := NewRPCClient(RPCClientDeps{URL: primary.URL()})
	one, err := single.CallEach(context.Background(), "eth_getTransactionCount", "0xabc", "pending")
	require.NoError(t, err)
	assert.Equal(t, []json.RawMessage{json.RawMessage(`"0x5"`)}, one)
}

func TestFailover_MethodNotServed_IsRememberedPerEndpointOnly(t *testing.T) {
	var primaryDown atomic.Bool
	primaryDown.Store(true)
	primary := newFakeNode(t, func(string) (int, string) {
		if primaryDown.Load() {
			return http.StatusServiceUnavailable, "down"
		}
		return result(`[]`)
	})
	oldNode := newFakeNode(t, func(method string) (int, string) {
		if method == "eth_getBlockReceipts" {
			return rpcFailure(-32601, "the method eth_getBlockReceipts does not exist")
		}
		return result(`"0x20"`)
	})
	client, _, _ := failoverClient(primary.URL(), oldNode.URL())

	err := client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1")
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	primaryDown.Store(false)
	require.NoError(t, client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1"), "the primary still serves it")

	primaryDown.Store(true)
	_ = client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1")
	assert.Equal(t, 1, oldNode.count("eth_getBlockReceipts"), "an endpoint that does not serve a method is not asked it again")
	require.NoError(t, client.Call(context.Background(), "eth_blockNumber", nil), "it serves the other methods")
}

func TestFailover_BlockReceipts_InvalidParamsIsAMethodNotServed(t *testing.T) {
	primary := newFakeNode(t, answers(rpcFailure(-32602, "invalid argument 0: json: cannot unmarshal hex string")))
	fallback := newFakeNode(t, answers(result(`[]`)))
	client, _, _ := failoverClient(primary.URL(), fallback.URL())

	require.NoError(t, client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1"))
	require.NoError(t, client.Call(context.Background(), "eth_getBlockReceipts", nil, "0x1"))
	assert.Equal(t, 1, primary.count("eth_getBlockReceipts"))
	assert.Equal(t, 2, fallback.count("eth_getBlockReceipts"))
}

func TestFailover_BlockReceipts_NullAsksOneMoreEndpoint(t *testing.T) {
	lagging := newFakeNode(t, answers(result(`null`)))
	synced := newFakeNode(t, answers(result(`[{"transactionHash":"0x1"}]`)))
	client, _, _ := failoverClient(lagging.URL(), synced.URL())

	var receipts []map[string]string
	require.NoError(t, client.Call(context.Background(), "eth_getBlockReceipts", &receipts, "0x1"))
	assert.Len(t, receipts, 1)
}

func TestFailover_CredentialsAndHeadersGoToThePrimaryOnly(t *testing.T) {
	type seen struct {
		auth, key string
	}
	record := func(into *[]seen, status int) *httptest.Server {
		var mu sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*into = append(*into, seen{auth: r.Header.Get("Authorization"), key: r.Header.Get("x-api-key")})
			mu.Unlock()
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		}))
		t.Cleanup(server.Close)
		return server
	}
	var atPrimary, atFallback []seen
	primary := record(&atPrimary, http.StatusBadGateway)
	fallback := record(&atFallback, http.StatusOK)
	client := NewRPCClient(RPCClientDeps{URL: primary.URL, User: "node", Password: "secret", Fallbacks: fallbackList(fallback.URL)}).
		WithHeader("x-api-key", "primary-key")

	require.NoError(t, client.Call(context.Background(), "getblockcount", nil))
	require.Len(t, atPrimary, 1)
	require.Len(t, atFallback, 1)
	assert.NotEmpty(t, atPrimary[0].auth)
	assert.Equal(t, "primary-key", atPrimary[0].key)
	assert.Empty(t, atFallback[0].auth, "the primary's basic auth never reaches another provider")
	assert.Empty(t, atFallback[0].key, "the primary's API key never reaches another provider")
}

func TestFailover_Health_TheCallersDeadlineIsNotTheEndpointsFailure(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	client := NewRPCClient(RPCClientDeps{URL: hanging.URL, ChainID: "eth"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	require.Error(t, client.Call(ctx, "eth_blockNumber", nil))
	assert.Zero(t, healthOf(client)[0].ConsecutiveFailures, "the call ran out of the caller's time, not the endpoint's")
}
