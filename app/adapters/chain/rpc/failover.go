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

	// An endpoint that refuses a call's shape (shapeKey) this many times in a row is
	// not asked that shape again for shapeNotServedTTL, unless no other endpoint
	// would be asked. Any other answer to that shape starts the count again.
	shapeRefusalsToRemember = 2
	shapeNotServedTTL       = 10 * time.Minute
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
	"eth_getBlockReceipts":      true,
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
	// notServed holds, per call shape, the refusals in a row and until when the
	// endpoint is not asked that shape.
	notServed map[string]shapeRefusal
}

type shapeRefusal struct {
	refusals int
	until    time.Time
}

// endpointSet is the ordered list a client calls: the primary first, each member
// with its position in the stored list (positions[0] is 1). It is immutable; a
// replacement installs a new set.
type endpointSet struct {
	members   []*endpoint
	positions []int

	// primaryNetwork is the identity the primary answered, once it did; the
	// fallbacks must answer the same.
	identityMu     sync.Mutex
	primaryNetwork string
	primaryAskedAt time.Time
	mismatchLogged bool
	// identityAsking is closed when the question to the primary in flight ends; nil
	// when none is.
	identityAsking chan struct{}
}

// newEndpointSet orders primary and fallbacks, dropping blanks and repeats. An
// endpoint of previous with the same URL is kept with its health. The primary
// drops what a runtime identity check found (it is not checked at runtime: its
// network was checked when it was written), and when the primary changes every
// fallback is checked again, against the new primary's network.
func newEndpointSet(previous *endpointSet, primary string, fallbacks []chain.RPCFallback) *endpointSet {
	known := map[string]*endpoint{}
	samePrimary := false
	if previous != nil {
		for _, member := range previous.members {
			known[member.url] = member
		}
		samePrimary = len(previous.members) > 0 && previous.members[0].url == strings.TrimSpace(primary)
	}
	set := &endpointSet{}
	seen := map[string]bool{}
	for i, next := range append([]chain.RPCFallback{{URL: primary, Position: 1}}, fallbacks...) {
		rawURL := strings.TrimSpace(next.URL)
		if rawURL == "" || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		position := next.Position
		if len(set.members) == 0 {
			position = 1
		} else if position <= 1 {
			position = i + 1
		}
		member, ok := known[rawURL]
		if !ok {
			member = &endpoint{url: rawURL, host: chain.RPCEndpointHost(rawURL), fingerprint: chain.RPCEndpointFingerprint(rawURL)}
		}
		if len(set.members) == 0 || !samePrimary {
			member.clearIdentity()
		}
		set.members = append(set.members, member)
		set.positions = append(set.positions, position)
	}
	return set
}

// candidate is an endpoint with its place in the set (0 is the primary) and its
// position in the stored list (what logs and health show).
type candidate struct {
	ep       *endpoint
	index    int
	position int
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
		usable = append(usable, candidate{ep: member, index: i, position: s.positions[i]})
		if !open {
			ready = append(ready, candidate{ep: member, index: i, position: s.positions[i]})
		}
	}
	if len(ready) == 0 {
		return usable
	}
	return ready
}

// others is every endpoint a second opinion about first's answer may come from:
// the usable ones (never a fallback on another network), cooling down or not, the
// primary first.
func (s *endpointSet) others(first candidate) []candidate {
	out := make([]candidate, 0, len(s.members))
	for i, member := range s.members {
		if i == first.index || member == first.ep {
			continue
		}
		if _, wrong := member.identity(); wrong && i > 0 {
			continue
		}
		out = append(out, candidate{ep: member, index: i, position: s.positions[i]})
	}
	return out
}

// find is target as a candidate of this set; one no longer in it keeps a place
// after the last member.
func (s *endpointSet) find(target *endpoint) candidate {
	for i, member := range s.members {
		if member == target || member.url == target.url {
			return candidate{ep: member, index: i, position: s.positions[i]}
		}
	}
	return candidate{ep: target, index: len(s.members), position: len(s.members) + 1}
}

// snapshot is the health of every endpoint, in order.
func (s *endpointSet) snapshot() []chain.RPCEndpointHealth {
	out := make([]chain.RPCEndpointHealth, 0, len(s.members))
	for i, member := range s.members {
		member.mu.Lock()
		out = append(out, chain.RPCEndpointHealth{
			Position:            s.positions[i],
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

// serves reports whether the endpoint may be asked shape now: it is not remembered
// as refusing it. A call without a shape is always served.
func (e *endpoint) serves(shape string, now time.Time) bool {
	if shape == "" {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return !now.Before(e.notServed[shape].until)
}

// refusedShape counts a refusal of shape; the shapeRefusalsToRemember-th in a row
// is remembered for shapeNotServedTTL. An expired memory counts from zero again.
func (e *endpoint) refusedShape(shape string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.notServed == nil {
		e.notServed = map[string]shapeRefusal{}
	}
	entry := e.notServed[shape]
	if !entry.until.IsZero() && !now.Before(entry.until) {
		entry = shapeRefusal{}
	}
	entry.refusals++
	if entry.refusals >= shapeRefusalsToRemember {
		entry = shapeRefusal{until: now.Add(shapeNotServedTTL)}
	}
	e.notServed[shape] = entry
}

// servedShape forgets the refusals of shape: the next one is not consecutive.
func (e *endpoint) servedShape(shape string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.notServed, shape)
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

// markIdentity records what an identity check found. Another network is sticky:
// a concurrent check that passed does not clear it for the life of the set.
func (e *endpoint) markIdentity(wrong bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if wrong {
		e.verified, e.wrongNetwork = false, true
		e.lastError = "wrong network"
		return
	}
	if !e.wrongNetwork {
		e.verified = true
	}
}

// call runs method on the endpoints in order (see RPCClient.Call). The returned
// error is the last endpoint's, with that endpoint's URL removed.
func (c *RPCClient) call(ctx context.Context, set *endpointSet, method string, out interface{}, params []interface{}) error {
	pin := pinFrom(ctx)
	candidates, pinned := c.candidates(set, pin)
	shape := shapeKey(method, params)
	candidates = servingCandidates(candidates, shape, c.now())
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
		result, kind, err := c.attempt(ctx, cand.ep.url, method, params, remaining, broadcast, remaining == 1 || backsOffEverywhere(ctx))
		if shape != "" {
			switch {
			case shapeRefused(err):
				cand.ep.refusedShape(shape, c.now())
				kind = outcomeUnsupported
			case err == nil || kind == outcomeAnswer:
				cand.ep.servedShape(shape)
			}
		}
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
			if method == solanaGetBlock && cand.index > 0 && !pinned && skippedSlotAnswer(err) {
				return c.confirmSkipped(ctx, set, cand, params, out, err, primaryLost, pin)
			}
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

// secondOpinionTimeout bounds the one extra question a null or a skipped slot asks,
// whatever the caller's deadline: a poll is not held for the HTTP timeout.
const secondOpinionTimeout = 5 * time.Second

// solanaGetBlock is the Solana lookup whose "skipped" answer a fallback alone may not give.
const solanaGetBlock = "getBlock"

// opinion is what a second endpoint answered; asked is false when none could be asked.
type opinion struct {
	from   candidate
	result json.RawMessage
	err    error
	asked  bool
}

// secondOpinion asks one more endpoint, the next in turn among rest, within
// secondOpinionTimeout and without backoff. A fallback that rate-limits the
// question or does not serve the method is not counted as failing: it was a second
// opinion, not the call.
func (c *RPCClient) secondOpinion(ctx context.Context, set *endpointSet, rest []candidate, method string, params []interface{}) opinion {
	if len(rest) == 0 {
		return opinion{}
	}
	return c.ask(ctx, set, rest[(c.secondOpinions.Add(1)-1)%uint64(len(rest))], method, params)
}

// ask puts one question to next, within secondOpinionTimeout and without backoff.
func (c *RPCClient) ask(ctx context.Context, set *endpointSet, next candidate, method string, params []interface{}) opinion {
	askCtx, cancel := context.WithTimeout(ctx, secondOpinionTimeout)
	defer cancel()
	if next.index > 0 {
		if err := c.verifyIdentity(askCtx, set, next, 1); err != nil {
			return opinion{from: next, err: err}
		}
	}
	result, kind, err := c.exchange(askCtx, next.ep.url, method, params, false)
	switch {
	case err == nil || kind == outcomeAnswer:
		c.succeeded(set, next)
	case kind == outcomeUnavailable && ctx.Err() == nil:
		c.failed(set, next, kind, err)
	}
	return opinion{from: next, result: result, err: err, asked: true}
}

// chaseNull asks one more endpoint for a lookup first answered null by first. A
// non-null answer is the result; otherwise first's null stands.
func (c *RPCClient) chaseNull(ctx context.Context, set *endpointSet, first candidate, rest []candidate, method string, params []interface{}, out interface{}, null json.RawMessage, primaryLost bool, pin *endpointPin) error {
	second := c.secondOpinion(ctx, set, rest, method, params)
	if second.asked && second.err == nil && !isJSONNull(second.result) {
		c.served(set, second.from, primaryLost)
		pin.hold(second.from.ep)
		return unmarshalResult(second.result, out)
	}
	c.served(set, first, primaryLost)
	pin.hold(first.ep)
	return unmarshalResult(null, out)
}

// confirmSkipped asks the other endpoints, the primary first and cooling down or
// not, about a slot a fallback answered skipped: a fallback may only miss the slot
// (a snapshot gap, a short history). The first block is the result; the first
// "skipped" confirms the answer. When none can say, the slot is unconfirmed
// (chain.ErrSkippedSlotUnconfirmed), an error the scan keeps pending, never a
// skipped slot.
func (c *RPCClient) confirmSkipped(ctx context.Context, set *endpointSet, first candidate, params []interface{}, out interface{}, skipped error, primaryLost bool, pin *endpointPin) error {
	for _, other := range set.others(first) {
		second := c.ask(ctx, set, other, solanaGetBlock, params)
		switch {
		case second.err == nil && !isJSONNull(second.result):
			c.served(set, second.from, primaryLost)
			pin.hold(second.from.ep)
			return unmarshalResult(second.result, out)
		case skippedSlotAnswer(second.err):
			c.served(set, first, primaryLost)
			pin.hold(first.ep)
			return scrub(first.ep.url, skipped)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return chain.Unavailable(fmt.Errorf("rpc call %s: endpoint %d of %s answered the slot skipped and no other endpoint could confirm it: %w",
		solanaGetBlock, first.position, c.chainID, chain.ErrSkippedSlotUnconfirmed))
}

// CallOthers asks method of every endpoint ready now other than the one ctx is
// pinned to (the primary when it is not pinned), at once, each within budget, and
// returns the results of those that answered, in no set order. A fallback is checked
// for its network first. It never fails: an endpoint that does not answer gives
// nothing and counts a failure, unless budget ran out first. With one endpoint it
// asks nobody.
func (c *RPCClient) CallOthers(ctx context.Context, budget time.Duration, method string, params ...interface{}) []json.RawMessage {
	if c == nil {
		return nil
	}
	if params == nil {
		params = []interface{}{}
	}
	set := c.endpoints.Load()
	if set == nil || len(set.members) < 2 {
		return nil
	}
	skip := set.members[0].url
	if held := pinFrom(ctx).held(); held != nil {
		skip = held.url
	}
	var others []candidate
	for _, cand := range set.ordered(c.now()) {
		if cand.ep.url != skip {
			others = append(others, cand)
		}
	}
	results := make([]json.RawMessage, len(others))
	var wg sync.WaitGroup
	for i, cand := range others {
		wg.Add(1)
		go func(i int, cand candidate) {
			defer wg.Done()
			askCtx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			if cand.index > 0 {
				if err := c.verifyIdentity(askCtx, set, cand, 1); err != nil {
					return
				}
			}
			result, kind, err := c.exchange(askCtx, cand.ep.url, method, params, false)
			switch {
			case err == nil || kind == outcomeAnswer:
				c.succeeded(set, cand)
			case kind != outcomeUnsupported && askCtx.Err() == nil:
				c.failed(set, cand, kind, err)
			}
			if err == nil {
				results[i] = result
			}
		}(i, cand)
	}
	wg.Wait()
	var answered []json.RawMessage
	for _, result := range results {
		if result != nil {
			answered = append(answered, result)
		}
	}
	return answered
}

// servingCandidates drops the endpoints remembered as refusing shape. When that
// would drop them all, every one is asked: the last endpoint that might serve the
// call is never skipped.
func servingCandidates(candidates []candidate, shape string, now time.Time) []candidate {
	serving := candidates[:0:0]
	for _, cand := range candidates {
		if cand.ep.serves(shape, now) {
			serving = append(serving, cand)
		}
	}
	if len(serving) == 0 {
		return candidates
	}
	return serving
}

// candidates is the pinned endpoint alone when the context holds one, else the
// set's order.
func (c *RPCClient) candidates(set *endpointSet, pin *endpointPin) ([]candidate, bool) {
	if held := pin.held(); held != nil {
		return []candidate{set.find(held)}, true
	}
	return set.ordered(c.now()), false
}

// attempt runs one exchange on one endpoint inside its share of the deadline. A
// broadcast keeps the caller's whole deadline: it never moves on after a timeout,
// so cutting it short would only turn an answer into an unknown outcome.
// retryRateLimits waits out a rate limit on this endpoint (the last one, or every
// one under BackOffOnRateLimits) instead of moving on.
func (c *RPCClient) attempt(ctx context.Context, rawURL, method string, params []interface{}, remaining int, broadcast, retryRateLimits bool) (json.RawMessage, outcome, error) {
	attemptCtx, cancel := attemptContext(ctx, remaining, broadcast)
	defer cancel()
	return c.exchange(attemptCtx, rawURL, method, params, retryRateLimits)
}

type backoffKey struct{}

// BackOffOnRateLimits returns a context whose calls wait out a rate limit on each
// endpoint, the backoff one endpoint alone gets, before moving to the next: for a
// read whose answer should come from the primary even while it is only slow to
// serve (the pending nonce).
func BackOffOnRateLimits(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, backoffKey{}, true)
}

func backsOffEverywhere(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	on, _ := ctx.Value(backoffKey{}).(bool)
	return on
}

func attemptContext(ctx context.Context, remaining int, broadcast bool) (context.Context, context.CancelFunc) {
	deadline, bounded := ctx.Deadline()
	if remaining <= 1 || broadcast || !bounded {
		return ctx, func() {}
	}
	share := time.Until(deadline) / time.Duration(remaining)
	return context.WithTimeout(ctx, max(share, failoverMinAttempt))
}

// verifyIdentity asks a fallback which network it serves before its first use: it
// must be the chain's recorded network and, once the primary answered its own, the
// primary's. A fallback on another network is never used again for the life of the
// list and is logged at error level; one that cannot answer counts a failure and is
// asked next time. When the primary itself serves another network than the record
// says (moved, not yet aligned), no fallback is used and that is logged once.
func (c *RPCClient) verifyIdentity(ctx context.Context, set *endpointSet, cand candidate, remaining int) error {
	if c.identity == nil {
		return nil
	}
	primary := c.primaryIdentity(ctx, set)
	if primary != "" && c.network != "" && primary != c.network {
		set.identityMu.Lock()
		first := !set.mismatchLogged
		set.mismatchLogged = true
		set.identityMu.Unlock()
		if first {
			slog.Error("rpc primary endpoint serves another network than the chain records; fallbacks are not used",
				"chain", c.chainID, "host", set.members[0].host)
		}
		return fmt.Errorf("rpc endpoint %d of %s: the primary serves another network than the chain records: %w", cand.position, c.chainID, chain.ErrWrongNetwork)
	}
	if verified, wrong := cand.ep.identity(); verified {
		return nil
	} else if wrong {
		return fmt.Errorf("rpc endpoint %d of %s: %w", cand.position, c.chainID, chain.ErrWrongNetwork)
	}
	attemptCtx, cancel := attemptContext(ctx, remaining, false)
	defer cancel()
	network, err := c.identity(attemptCtx, endpointCaller{client: c, url: cand.ep.url})
	if err != nil {
		err = scrub(cand.ep.url, err)
		// The caller's own deadline is not the endpoint's failure.
		if ctx.Err() == nil {
			c.failed(set, cand, outcomeUnavailable, err)
		}
		return err
	}
	want := c.network
	if want == "" {
		want = primary
	}
	if want == "" || network != want || (primary != "" && network != primary) {
		cand.ep.markIdentity(true)
		slog.Error("rpc fallback endpoint serves another network and is not used", "chain", c.chainID, "position", cand.position, "host", cand.ep.host)
		c.report(set)
		return fmt.Errorf("rpc endpoint %d of %s: %w", cand.position, c.chainID, chain.ErrWrongNetwork)
	}
	cand.ep.markIdentity(false)
	return nil
}

const (
	// primaryIdentityTimeout bounds the question asked of the primary about its network.
	primaryIdentityTimeout = 5 * time.Second
	// primaryIdentityRetry spaces the questions while the primary does not answer,
	// so a primary that hangs costs one bounded wait per interval, not one per call.
	primaryIdentityRetry = endpointCooldownBase
)

// primaryIdentity is the network the primary of set answers, kept once it answered;
// "" while it has not. The question runs in the background under its own deadline
// (primaryIdentityTimeout) and is asked again after primaryIdentityRetry while it gets
// no answer. A caller waits for it at most half of its own time left, so a primary
// that hangs never takes the time a fallback needs to answer.
func (c *RPCClient) primaryIdentity(ctx context.Context, set *endpointSet) string {
	if done := c.askPrimaryIdentity(set); done != nil {
		wait := primaryIdentityTimeout
		if deadline, bounded := ctx.Deadline(); bounded {
			wait = min(wait, time.Until(deadline)/2)
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-done:
			case <-timer.C:
			case <-ctx.Done():
			}
			timer.Stop()
		}
	}
	set.identityMu.Lock()
	defer set.identityMu.Unlock()
	return set.primaryNetwork
}

// askPrimaryIdentity starts the question to the primary when it is due and returns
// a channel closed when the question in flight ends; nil when the answer is known or
// no question is due.
func (c *RPCClient) askPrimaryIdentity(set *endpointSet) <-chan struct{} {
	now := c.now()
	set.identityMu.Lock()
	defer set.identityMu.Unlock()
	switch {
	case set.primaryNetwork != "" || len(set.members) == 0:
		return nil
	case set.identityAsking != nil:
		return set.identityAsking
	case !set.primaryAskedAt.IsZero() && now.Sub(set.primaryAskedAt) < primaryIdentityRetry:
		return nil
	}
	set.primaryAskedAt = now
	done := make(chan struct{})
	set.identityAsking = done
	primary := set.members[0].url
	go func() {
		defer close(done)
		askCtx, cancel := context.WithTimeout(context.Background(), primaryIdentityTimeout)
		defer cancel()
		network, err := c.identity(askCtx, endpointCaller{client: c, url: primary})
		set.identityMu.Lock()
		defer set.identityMu.Unlock()
		set.identityAsking = nil
		if network = strings.TrimSpace(network); err == nil && network != "" && set.primaryNetwork == "" {
			set.primaryNetwork = network
		}
	}()
	return done
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
			"chain", c.chainID, "position", cand.position, "host", cand.ep.host, "error", label, "skipped_for", cooldown.String())
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
			"chain", c.chainID, "primary", set.members[0].host, "serving", cand.ep.host, "position", cand.position)
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
