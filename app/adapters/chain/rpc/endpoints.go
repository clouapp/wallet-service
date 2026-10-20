package rpc

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// failover is an ordered endpoint list with its health and network checks, what
// RPCClient and Endpoints share: the order calls take, the cool-downs, the identity
// check of a fallback before its first use, the health records and the logs of a
// switch away from the primary and back.
type failover struct {
	endpoints atomic.Pointer[endpointSet] // replaced without logging a URL
	chainID   string
	// identity asks one endpoint which network it serves; nil checks none.
	identity   func(ctx context.Context, rawURL string) (string, error)
	network    string
	health     chain.RPCHealthSink
	now        func() time.Time
	onFallback atomic.Bool
}

func (c *failover) init(primary string, fallbacks []chain.RPCFallback, chainID string, identity func(context.Context, string) (string, error), network string, health chain.RPCHealthSink) {
	c.chainID = strings.TrimSpace(chainID)
	c.identity = identity
	c.network = strings.TrimSpace(network)
	c.health = health
	c.now = time.Now
	c.endpoints.Store(newEndpointSet(nil, primary, fallbacks))
}

// primaryURL is the URL of the primary endpoint; callers must not log it.
func (c *failover) primaryURL() string {
	set := c.endpoints.Load()
	if set == nil || len(set.members) == 0 {
		return ""
	}
	return set.members[0].url
}

// fallbacks are the endpoints after the primary, in order, with their positions.
func (c *failover) fallbacks() []chain.RPCFallback {
	set := c.endpoints.Load()
	if set == nil || len(set.members) < 2 {
		return nil
	}
	fallbacks := make([]chain.RPCFallback, 0, len(set.members)-1)
	for i, member := range set.members[1:] {
		fallbacks = append(fallbacks, chain.RPCFallback{URL: member.url, Position: set.positions[i+1]})
	}
	return fallbacks
}

// replace installs primary and fallbacks, in order. An endpoint already in the list
// keeps its health; an empty primary is ignored.
func (c *failover) replace(primary string, fallbacks []chain.RPCFallback) {
	if strings.TrimSpace(primary) == "" {
		return
	}
	c.endpoints.Store(newEndpointSet(c.endpoints.Load(), primary, fallbacks))
	c.onFallback.Store(false)
}

// Outcome is what one answer of one endpoint means for failover.
type Outcome = outcome

const (
	// OutcomeAnswer is an answer about the request, a result or a refusal any
	// endpoint would give: it is returned and the endpoint is healthy.
	OutcomeAnswer = outcomeAnswer
	// OutcomeUnsupported is an endpoint that does not serve the call: the call moves
	// on and the endpoint stays healthy.
	OutcomeUnsupported = outcomeUnsupported
	// OutcomeUnavailable is an endpoint that could not answer (transport error,
	// timeout, HTTP 5xx, an unreadable body, a node that is not synced): the call
	// moves on and the endpoint counts a failure.
	OutcomeUnavailable = outcomeUnavailable
	// OutcomeRateLimited is an endpoint that refused the call for capacity: it was
	// not served, the call moves on and the endpoint counts a failure.
	OutcomeRateLimited = outcomeRateLimited
)

// Attempt is one endpoint a call is put to. URL must never be logged. Primary is
// the chain's primary endpoint (the only one an API key header goes to); Last is
// the last endpoint the call may try, which waits out a rate limit instead of
// moving on.
type Attempt struct {
	URL     string
	Primary bool
	Last    bool
}

// Exchange puts one call to one endpoint and says what its answer means for
// failover: nil with OutcomeAnswer when it answered, or the error and its outcome.
type Exchange func(ctx context.Context, at Attempt) (Outcome, error)

// Endpoints is the endpoint list of an adapter that speaks its own protocol over
// HTTP (the TRON HTTP API, rippled JSON-RPC) with the failover of RPCClient: Do puts
// a call to the primary and then to the fallbacks in order, each fallback asked
// first which network it serves, with the same health, cool-downs, logs and
// broadcast rule.
type Endpoints struct {
	failover
}

// EndpointsDeps is the list NewEndpoints keeps. Identity asks one endpoint which
// network it serves, as a token: a fallback serves only once its answer equals
// Network (the chain's recorded network) and the primary's own answer. Health
// receives the endpoint health each time it changes.
type EndpointsDeps struct {
	URL       string
	Fallbacks []chain.RPCFallback
	ChainID   string
	Identity  func(ctx context.Context, rawURL string) (string, error)
	Network   string
	Health    chain.RPCHealthSink
}

// NewEndpoints builds the list. It does not dial any endpoint.
func NewEndpoints(deps EndpointsDeps) *Endpoints {
	endpoints := &Endpoints{}
	endpoints.init(deps.URL, deps.Fallbacks, deps.ChainID, deps.Identity, deps.Network, deps.Health)
	return endpoints
}

// Endpoint is the URL of the primary endpoint. Callers must not log it.
func (e *Endpoints) Endpoint() string {
	if e == nil {
		return ""
	}
	return e.primaryURL()
}

// Fallbacks are the endpoints tried after the primary, in order, with their
// positions. Callers must not log the URLs.
func (e *Endpoints) Fallbacks() []chain.RPCFallback {
	if e == nil {
		return nil
	}
	return e.fallbacks()
}

// ReplaceEndpoints installs primary and fallbacks, in order. An endpoint already in
// the list keeps its health; an empty primary is ignored. No URL is logged.
func (e *Endpoints) ReplaceEndpoints(primary string, fallbacks []chain.RPCFallback) {
	if e == nil {
		return
	}
	e.replace(primary, fallbacks)
}

// Health is what this process last saw of every endpoint, in order.
func (e *Endpoints) Health() []chain.RPCEndpointHealth {
	if e == nil {
		return nil
	}
	set := e.endpoints.Load()
	if set == nil {
		return nil
	}
	return set.snapshot()
}

// Do puts exchange to the endpoints in order and returns the first answer: nil, or
// the error of an answer about the request (OutcomeAnswer), which another endpoint
// would give too. An endpoint that does not serve the call, could not answer or
// rate-limited it is left for the next one. A broadcast moves on only while the
// endpoint certainly did not take the bytes (a connection never made, a call marked
// chain.NotSent, a rate limit, a call not served): any other failure may have handed
// them over and is returned as it is; when no endpoint took them the error is marked
// chain.NotSent. A fallback is asked which network it serves before its first use
// and is never used on another network. Inside a PinEndpoint scope every call goes
// to the endpoint that answered first. The error is the last endpoint's, with its
// URL removed.
func (e *Endpoints) Do(ctx context.Context, broadcast bool, exchange Exchange) error {
	if e == nil || exchange == nil {
		return fmt.Errorf("rpc call: endpoints are not configured")
	}
	set := e.endpoints.Load()
	if set == nil || len(set.members) == 0 {
		return fmt.Errorf("rpc call: no endpoint is configured")
	}
	pin := pinFrom(ctx)
	candidates, pinned := e.candidates(set, pin)
	var (
		lastErr     error
		primaryLost = !pinned && len(candidates) > 0 && candidates[0].index != 0
	)
	for i, cand := range candidates {
		remaining := len(candidates) - i
		if cand.index > 0 && !pinned {
			if err := e.verifyIdentity(ctx, set, cand, remaining); err != nil {
				if lastErr == nil {
					lastErr = err
				}
				if ctx.Err() != nil {
					break
				}
				continue
			}
		}
		attemptCtx, cancel := attemptContext(ctx, remaining, broadcast)
		kind, err := exchange(attemptCtx, Attempt{URL: cand.ep.url, Primary: cand.index == 0, Last: remaining == 1})
		cancel()
		if err == nil || kind == outcomeAnswer {
			e.succeeded(set, cand)
			e.served(set, cand, primaryLost)
			pin.hold(cand.ep)
			return httpclient.RedactURL(err, cand.ep.url)
		}
		// The caller's own deadline is not the endpoint's failure.
		if kind != outcomeUnsupported && ctx.Err() == nil {
			e.failed(set, cand, kind, err)
		}
		lastErr = httpclient.RedactURL(err, cand.ep.url)
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
		lastErr = fmt.Errorf("rpc call: no usable endpoint")
	}
	if broadcast {
		return chain.NotSent(lastErr)
	}
	return lastErr
}

// Any asks every endpoint, in order and cooling down or not, until ask finds what it
// looks for (a transaction known to one of them): true when one did. A fallback is
// asked which network it serves first and is never asked on another network. An
// endpoint that could not answer is passed; the error is the last one only when no
// endpoint answered at all. Health is not changed: it is a search, not a call.
func (e *Endpoints) Any(ctx context.Context, ask func(ctx context.Context, at Attempt) (bool, error)) (bool, error) {
	if e == nil || ask == nil {
		return false, fmt.Errorf("rpc search: endpoints are not configured")
	}
	set := e.endpoints.Load()
	if set == nil || len(set.members) == 0 {
		return false, fmt.Errorf("rpc search: no endpoint is configured")
	}
	var lastErr error
	answered := false
	for i, member := range set.members {
		cand := candidate{ep: member, index: i, position: set.positions[i]}
		if i > 0 {
			if err := e.verifyIdentity(ctx, set, cand, 1); err != nil {
				lastErr = err
				continue
			}
		}
		found, err := ask(ctx, Attempt{URL: member.url, Primary: i == 0, Last: i == len(set.members)-1})
		if err != nil {
			lastErr = httpclient.RedactURL(err, member.url)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if found {
			return true, nil
		}
		answered = true
	}
	if answered {
		return false, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("rpc search: no usable endpoint")
	}
	return false, lastErr
}
