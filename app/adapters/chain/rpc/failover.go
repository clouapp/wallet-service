package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	// An endpoint that fails this many calls in a row is skipped for a cool-down that
	// starts at endpointCooldownBase and doubles on every trip, up to
	// endpointCooldownMax. After a cool-down one more failure skips it again (the
	// next, longer, cool-down); a success clears it. The Bitcoin-family failover
	// uses the same values.
	endpointFailuresToOpen = 3
	endpointCooldownBase   = 30 * time.Second
	endpointCooldownMax    = 5 * time.Minute
	endpointMaxTripShift   = 8

	// Under a caller deadline, an attempt that has another endpoint behind it gets
	// its share of the time left (never under failoverMinAttempt), so a node that
	// hangs leaves time to ask the next one; the last endpoint keeps what is left.
	// Without a deadline every attempt keeps the HTTP client's timeout, as with one
	// endpoint: a large answer (a 64 MiB EVM block, a busy Solana slot) is not cut.
	failoverMinAttempt = 2 * time.Second
)

// Methods whose answer is a signed transaction handed to the network. They move to
// the next endpoint only when the previous one certainly did not accept the bytes
// (notAccepted); a refusal "already known" from a later endpoint is a success.
var broadcastMethods = map[string]bool{
	"eth_sendRawTransaction": true,
	"sendTransaction":        true,
}

// Lookups whose null result means "this node does not have it" (a node behind the
// tip, or a load-balanced backend without the index): one more healthy endpoint is
// asked before the null is believed, a different one on each call (chaseNull), so a
// lookup polled until it exists asks every endpoint in turn without multiplying the
// load on each poll.
var nullMeansAskNext = map[string]bool{
	"eth_getTransactionReceipt": true,
	"eth_getTransactionByHash":  true,
	"eth_getBlockByNumber":      true,
	"eth_getBlockByHash":        true,
	"getTransaction":            true,
	"getBlock":                  true,
}

// endpoint is one URL of a client with its health. The URL is never logged; host
// is the only part that is.
type endpoint struct {
	url         string
	host        string
	fingerprint string

	mu                  sync.Mutex
	consecutiveFailures int
	trips               int
	openUntil           time.Time
	lastError           string
	lastErrorAt         time.Time
	verified            bool
	wrongNetwork        bool
}

// endpointSet is the ordered list a client calls: the primary first. It is
// immutable; a replacement installs a new set.
type endpointSet struct {
	members []*endpoint
}

// newEndpointSet orders primary and fallbacks, dropping blanks and repeats. An
// endpoint of previous with the same URL is kept with its health, except that the
// primary drops what a runtime identity check found: the primary is not checked at
// runtime (its network was checked when it was written), so a former fallback seen
// on another network is not kept out once it is the primary.
func newEndpointSet(previous *endpointSet, primary string, fallbacks []string) *endpointSet {
	known := map[string]*endpoint{}
	if previous != nil {
		for _, member := range previous.members {
			known[member.url] = member
		}
	}
	set := &endpointSet{}
	seen := map[string]bool{}
	for _, rawURL := range append([]string{primary}, fallbacks...) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		member, ok := known[rawURL]
		if !ok {
			member = &endpoint{url: rawURL, host: chain.RPCEndpointHost(rawURL), fingerprint: chain.RPCEndpointFingerprint(rawURL)}
		}
		if len(set.members) == 0 {
			member.clearIdentity()
		}
		set.members = append(set.members, member)
	}
	return set
}

// candidate is an endpoint with its place in the set (0 is the primary).
type candidate struct {
	ep    *endpoint
	index int
}

// ordered is the endpoints a call tries: the available ones in configured order,
// or, when every one is cooling down, all of them. A fallback that serves another
// network is never tried; the primary always may be.
func (s *endpointSet) ordered(now time.Time) []candidate {
	ready := make([]candidate, 0, len(s.members))
	usable := make([]candidate, 0, len(s.members))
	for i, member := range s.members {
		member.mu.Lock()
		wrong, open := member.wrongNetwork, now.Before(member.openUntil)
		member.mu.Unlock()
		if wrong && i > 0 {
			continue
		}
		usable = append(usable, candidate{ep: member, index: i})
		if !open {
			ready = append(ready, candidate{ep: member, index: i})
		}
	}
	if len(ready) == 0 {
		return usable
	}
	return ready
}

func (s *endpointSet) indexOf(target *endpoint) int {
	for i, member := range s.members {
		if member == target || member.url == target.url {
			return i
		}
	}
	return len(s.members)
}

// snapshot is the health of every endpoint, in order.
func (s *endpointSet) snapshot() []chain.RPCEndpointHealth {
	out := make([]chain.RPCEndpointHealth, 0, len(s.members))
	for i, member := range s.members {
		member.mu.Lock()
		out = append(out, chain.RPCEndpointHealth{
			Position:            i + 1,
			Host:                member.host,
			Fingerprint:         member.fingerprint,
			ConsecutiveFailures: member.consecutiveFailures,
			CoolingDownUntil:    member.openUntil,
			LastError:           member.lastError,
			LastErrorAt:         member.lastErrorAt,
			WrongNetwork:        member.wrongNetwork,
		})
		member.mu.Unlock()
	}
	return out
}

// succeed clears the endpoint's failures and reports whether it was failing.
func (e *endpoint) succeed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := e.consecutiveFailures > 0 || e.trips > 0
	e.consecutiveFailures = 0
	e.trips = 0
	e.openUntil = time.Time{}
	return changed
}

// fail counts a failure. It returns the cool-down it opened (zero when none) and
// whether the health changed. A failure inside an open cool-down (every endpoint
// was cooling down, so all were tried) only updates the last error.
func (e *endpoint) fail(now time.Time, label string) (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastError, e.lastErrorAt = label, now
	if now.Before(e.openUntil) {
		return 0, false
	}
	e.consecutiveFailures++
	if e.trips == 0 && e.consecutiveFailures < endpointFailuresToOpen {
		return 0, e.consecutiveFailures == 1
	}
	cooldown := min(endpointCooldownBase<<min(e.trips, endpointMaxTripShift), endpointCooldownMax)
	e.trips++
	e.openUntil = now.Add(cooldown)
	return cooldown, true
}

// identity reports what is known of the network the endpoint serves.
func (e *endpoint) identity() (verified, wrong bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.verified, e.wrongNetwork
}

// clearIdentity forgets what an identity check found.
func (e *endpoint) clearIdentity() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verified, e.wrongNetwork = false, false
}

func (e *endpoint) markIdentity(wrong bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verified = !wrong
	e.wrongNetwork = wrong
	if wrong {
		e.lastError = "wrong network"
	}
}

// call runs method on the endpoints in order (see RPCClient.Call). The returned
// error is the last endpoint's, with that endpoint's URL removed.
func (c *RPCClient) call(ctx context.Context, set *endpointSet, method string, out interface{}, params []interface{}) error {
	pin := pinFrom(ctx)
	candidates, pinned := c.candidates(set, pin)
	broadcast := broadcastMethods[method]
	askNextOnNull := nullMeansAskNext[method] && !pinned

	var (
		lastErr error
		// A pinned call follows the endpoint its scope started on; it says nothing
		// about the primary.
		primaryLost = !pinned && len(candidates) > 0 && candidates[0].index != 0
	)
	for i := range candidates {
		cand := candidates[i]
		remaining := len(candidates) - i
		if cand.index > 0 && !pinned {
			if err := c.verifyIdentity(ctx, set, cand, remaining); err != nil {
				// A fallback that could not be checked was not called: the error of a
				// send stays the one returned (a broadcast is classified by it).
				if lastErr == nil {
					lastErr = err
				}
				if ctx.Err() != nil {
					break
				}
				continue
			}
		}
		result, kind, err := c.attempt(ctx, cand.ep.url, method, params, remaining, broadcast)
		if err == nil {
			c.succeeded(set, cand)
			if askNextOnNull && remaining > 1 && isJSONNull(result) {
				return c.chaseNull(ctx, set, cand, candidates[i+1:], method, params, out, result, primaryLost, pin)
			}
			c.served(set, cand, primaryLost)
			pin.hold(cand.ep)
			return unmarshalResult(result, out)
		}
		if kind == outcomeAnswer {
			c.succeeded(set, cand)
			c.served(set, cand, primaryLost)
			pin.hold(cand.ep)
			if broadcast && i > 0 && alreadyKnown(err) {
				return &alreadyKnownError{cause: scrub(cand.ep.url, err)}
			}
			return scrub(cand.ep.url, err)
		}
		// The caller's own deadline is not the endpoint's failure.
		if kind != outcomeUnsupported && ctx.Err() == nil {
			c.failed(set, cand, kind, err)
		}
		lastErr = scrub(cand.ep.url, err)
		if cand.index == 0 {
			primaryLost = true
		}
		if pinned || (broadcast && !notAccepted(kind, err)) {
			return lastErr
		}
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("rpc call %s: no usable endpoint", method)
	}
	return lastErr
}

// chaseNull asks one more endpoint for a lookup first answered null by first: the
// next in turn among the healthy ones after it. A non-null answer is the result;
// otherwise first's null stands. The chase does not back off, and a fallback that
// rate-limits it or does not serve the method is not counted as failing: it was a
// second opinion, not the call.
func (c *RPCClient) chaseNull(ctx context.Context, set *endpointSet, first candidate, rest []candidate, method string, params []interface{}, out interface{}, null json.RawMessage, primaryLost bool, pin *endpointPin) error {
	next := rest[(c.nullChase.Add(1)-1)%uint64(len(rest))]
	believe := func() error {
		c.served(set, first, primaryLost)
		pin.hold(first.ep)
		return unmarshalResult(null, out)
	}
	if next.index > 0 {
		if err := c.verifyIdentity(ctx, set, next, 1); err != nil {
			return believe()
		}
	}
	result, kind, err := c.exchange(ctx, next.ep.url, method, params, false)
	switch {
	case err == nil && !isJSONNull(result):
		c.succeeded(set, next)
		c.served(set, next, primaryLost)
		pin.hold(next.ep)
		return unmarshalResult(result, out)
	case err == nil || kind == outcomeAnswer:
		c.succeeded(set, next)
	case kind == outcomeUnavailable && ctx.Err() == nil:
		c.failed(set, next, kind, err)
	}
	return believe()
}

// candidates is the pinned endpoint alone when the context holds one, else the
// set's order.
func (c *RPCClient) candidates(set *endpointSet, pin *endpointPin) ([]candidate, bool) {
	if held := pin.held(); held != nil {
		return []candidate{{ep: held, index: set.indexOf(held)}}, true
	}
	return set.ordered(c.now()), false
}

// attempt runs one exchange on one endpoint inside its share of the deadline. A
// broadcast keeps the caller's whole deadline: it never moves on after a timeout,
// so cutting it short would only turn an answer into an unknown outcome.
func (c *RPCClient) attempt(ctx context.Context, rawURL, method string, params []interface{}, remaining int, broadcast bool) (json.RawMessage, outcome, error) {
	attemptCtx, cancel := attemptContext(ctx, remaining, broadcast)
	defer cancel()
	return c.exchange(attemptCtx, rawURL, method, params, remaining == 1)
}

func attemptContext(ctx context.Context, remaining int, broadcast bool) (context.Context, context.CancelFunc) {
	deadline, bounded := ctx.Deadline()
	if remaining <= 1 || broadcast || !bounded {
		return ctx, func() {}
	}
	share := time.Until(deadline) / time.Duration(remaining)
	return context.WithTimeout(ctx, max(share, failoverMinAttempt))
}

// verifyIdentity asks a fallback which network it serves before its first use. A
// fallback on another network is never used again by this client and is logged at
// error level; one that cannot answer counts a failure and is asked next time.
func (c *RPCClient) verifyIdentity(ctx context.Context, set *endpointSet, cand candidate, remaining int) error {
	if c.identity == nil {
		return nil
	}
	if verified, wrong := cand.ep.identity(); verified {
		return nil
	} else if wrong {
		return fmt.Errorf("rpc endpoint %d of %s: %w", cand.index+1, c.chainID, chain.ErrWrongNetwork)
	}
	attemptCtx, cancel := attemptContext(ctx, remaining, false)
	defer cancel()
	err := c.identity(attemptCtx, endpointCaller{client: c, url: cand.ep.url})
	switch {
	case err == nil:
		cand.ep.markIdentity(false)
		return nil
	case errors.Is(err, chain.ErrWrongNetwork):
		cand.ep.markIdentity(true)
		slog.Error("rpc fallback endpoint serves another network and is not used", "chain", c.chainID, "position", cand.index+1, "host", cand.ep.host)
		c.report(set)
		return fmt.Errorf("rpc endpoint %d of %s: %w", cand.index+1, c.chainID, chain.ErrWrongNetwork)
	default:
		err = scrub(cand.ep.url, err)
		// The caller's own deadline is not the endpoint's failure.
		if ctx.Err() == nil {
			c.failed(set, cand, outcomeUnavailable, err)
		}
		return err
	}
}

func (c *RPCClient) succeeded(set *endpointSet, cand candidate) {
	if cand.ep.succeed() {
		c.report(set)
	}
}

func (c *RPCClient) failed(set *endpointSet, cand candidate, kind outcome, err error) {
	label := failureLabel(kind, err)
	cooldown, changed := cand.ep.fail(c.now(), label)
	if cooldown > 0 && len(set.members) > 1 {
		slog.Warn("rpc endpoint keeps failing, skipped for a cool-down",
			"chain", c.chainID, "position", cand.index+1, "host", cand.ep.host, "error", label, "skipped_for", cooldown.String())
	}
	if changed {
		c.report(set)
	}
}

// served notes which endpoint answered and logs a switch away from the primary
// (only when the primary failed or was skipped) and the way back to it.
func (c *RPCClient) served(set *endpointSet, cand candidate, primaryLost bool) {
	if len(set.members) < 2 {
		return
	}
	if cand.index == 0 {
		if c.onFallback.CompareAndSwap(true, false) {
			slog.Warn("rpc back on the primary endpoint", "chain", c.chainID, "host", set.members[0].host)
		}
		return
	}
	if primaryLost && c.onFallback.CompareAndSwap(false, true) {
		slog.Warn("rpc switched away from the primary endpoint",
			"chain", c.chainID, "primary", set.members[0].host, "serving", cand.ep.host, "position", cand.index+1)
	}
}

func (c *RPCClient) report(set *endpointSet) {
	if c.health == nil || c.chainID == "" {
		return
	}
	c.health.RecordRPCHealth(c.chainID, set.snapshot())
}

// endpointCaller reaches one endpoint of a client, with no failover. The identity
// check asks through it.
type endpointCaller struct {
	client *RPCClient
	url    string
}

func (e endpointCaller) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
	if params == nil {
		params = []interface{}{}
	}
	result, _, err := e.client.exchange(ctx, e.url, method, params, false)
	if err != nil {
		return scrub(e.url, err)
	}
	return unmarshalResult(result, out)
}

func isJSONNull(result json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(result), []byte("null"))
}

// ErrBroadcastAlreadyKnown is a broadcast that a later endpoint refused because the
// network already holds these exact bytes, after an earlier endpoint certainly did
// not accept them. The transaction was broadcast; its hash is the one computed
// locally from the signed bytes.
var ErrBroadcastAlreadyKnown = errors.New("transaction already known to the network")

type alreadyKnownError struct {
	cause error
}

func (e *alreadyKnownError) Error() string { return ErrBroadcastAlreadyKnown.Error() }

func (e *alreadyKnownError) Unwrap() error { return e.cause }

func (e *alreadyKnownError) Is(target error) bool { return target == ErrBroadcastAlreadyKnown }

type pinKey struct{}

// endpointPin holds the endpoint that answered first inside a PinEndpoint scope.
type endpointPin struct {
	mu sync.Mutex
	ep *endpoint
}

// PinEndpoint returns a context whose calls are all served by one endpoint: the
// first one that answers (with the usual failover before that). Once it answered,
// a failure of that endpoint fails the call instead of moving on, so a caller that
// reads one unit (a block, then its logs) never mixes the views of two nodes; it
// retries the unit, which may then start on another endpoint.
func PinEndpoint(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, pinKey{}, &endpointPin{})
}

func pinFrom(ctx context.Context) *endpointPin {
	if ctx == nil {
		return nil
	}
	pin, _ := ctx.Value(pinKey{}).(*endpointPin)
	return pin
}

func (p *endpointPin) held() *endpoint {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ep
}

func (p *endpointPin) hold(ep *endpoint) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ep == nil {
		p.ep = ep
	}
}
