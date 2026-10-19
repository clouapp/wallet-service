package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	rpcUserAgent        = "Macro-Wallets/0.1 RPC"
	rpcMaxResponseBytes = 1 << 20
	rpcHTTPTimeout      = 30 * time.Second
)

// RPCClient is a generic JSON-RPC 2.0 client.
// BTC, ETH, and SOL all speak JSON-RPC — only method names differ.
//
// A client has one endpoint (the primary) or an ordered list (the primary and its
// fallbacks). With a list, a call moves to the next endpoint when one cannot serve
// it (failover.go); with one endpoint every call behaves as it always did.
type RPCClient struct {
	endpoints        atomic.Pointer[endpointSet] // replaced without logging a URL
	client           *httpclient.Client
	requestID        atomic.Uint64
	username         string
	password         string
	headers          map[string]string
	retry            rateLimitRetry
	maxResponseBytes int

	chainID    string
	identity   IdentityReader
	network    string
	health     chain.RPCHealthSink
	now        func() time.Time
	onFallback atomic.Bool
	// secondOpinions turns the endpoint a null or a skipped slot asks next.
	secondOpinions atomic.Uint64
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      uint64        `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCError is the JSON-RPC error Call returns. The live Bitcoin adapter matches it.
type RPCError = rpcError

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// IdentityReader asks one endpoint which network it serves, through caller, which
// reaches that endpoint only, and returns it as a token (an EVM chain id, a Solana
// genesis hash). An error is a failure of the endpoint, which is asked again later.
type IdentityReader func(ctx context.Context, caller chain.JSONRPCCaller) (string, error)

// RPCClientDeps is the endpoint and optional basic-auth pair NewRPCClient stores.
// Empty User and Password send no basic auth. No URL is logged. The basic auth and
// the WithHeader headers belong to the primary (URL) and are sent to it only: a
// fallback is another provider, which carries its own key in its URL if it needs one.
//
// Fallbacks are tried in order after URL when it cannot serve a call. ChainID names
// the chain in logs and health records. With Identity set, a fallback serves only
// once its answer equals Network (the chain's recorded network) and the primary's
// own answer, read once per list: a primary pointed at another network than the
// record says keeps every fallback out. Health receives the endpoint health each
// time it changes.
type RPCClientDeps struct {
	URL       string
	User      string
	Password  string
	Fallbacks []chain.RPCFallback
	ChainID   string
	Identity  IdentityReader
	Network   string
	Health    chain.RPCHealthSink
}

func NewRPCClient(deps RPCClientDeps) *RPCClient {
	client := &RPCClient{
		username: deps.User,
		password: deps.Password,
		client:   httpclient.NewClient(rpcHTTPTimeout),
		retry:    defaultRateLimitRetry(),
		chainID:  strings.TrimSpace(deps.ChainID),
		identity: deps.Identity,
		network:  strings.TrimSpace(deps.Network),
		health:   deps.Health,
		now:      time.Now,
	}
	client.endpoints.Store(newEndpointSet(nil, deps.URL, deps.Fallbacks))
	return client
}

// WithMaxResponseBytes raises or lowers the size of the largest answer accepted.
// Zero keeps rpcMaxResponseBytes. A larger answer is an error, not a truncated body.
func (c *RPCClient) WithMaxResponseBytes(limit int) *RPCClient {
	if c == nil {
		return nil
	}
	c.maxResponseBytes = limit
	return c
}

// WithHeader sends name: value on every request to the primary endpoint (never to a
// fallback). An empty value is ignored.
// Header is the value WithHeader stored for name. Tests use it to confirm a key
// is attached to the client and not to a provider that must not see it.
func (c *RPCClient) Header(name string) string {
	if c == nil || c.headers == nil {
		return ""
	}
	return c.headers[name]
}

func (c *RPCClient) WithHeader(name, value string) *RPCClient {
	if c == nil || value == "" {
		return c
	}
	if c.headers == nil {
		c.headers = make(map[string]string)
	}
	c.headers[name] = value
	return c
}

// Endpoint is the URL of the primary endpoint. Callers must not log it.
func (c *RPCClient) Endpoint() string {
	if c == nil {
		return ""
	}
	set := c.endpoints.Load()
	if set == nil || len(set.members) == 0 {
		return ""
	}
	return set.members[0].url
}

// Fallbacks are the endpoints tried after the primary, in order, with their
// positions. Callers must not log the URLs.
func (c *RPCClient) Fallbacks() []chain.RPCFallback {
	if c == nil {
		return nil
	}
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

// ReplaceEndpoint points later calls at endpoint as the primary and keeps the
// fallbacks. An empty value is ignored so a failed read cannot wipe the current
// endpoint. The URL is not logged.
func (c *RPCClient) ReplaceEndpoint(endpoint string) {
	if c == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	c.ReplaceEndpoints(endpoint, c.Fallbacks())
}

// ReplaceEndpoints installs primary and fallbacks, in order. An endpoint already
// in the list keeps its health; an empty primary is ignored. No URL is logged.
func (c *RPCClient) ReplaceEndpoints(primary string, fallbacks []chain.RPCFallback) {
	if c == nil || strings.TrimSpace(primary) == "" {
		return
	}
	c.endpoints.Store(newEndpointSet(c.endpoints.Load(), primary, fallbacks))
	c.onFallback.Store(false)
}

// Call executes a JSON-RPC method and unmarshals result into `out`. A rate-limited
// request (HTTP 429/403 or JSON-RPC 429) was not served: with one endpoint it is
// retried with backoff, with several it moves to the next one (the last one tried
// keeps the backoff). Which other failures move to the next endpoint is decided in
// failover.go; with one endpoint every other failure is returned at once.
func (c *RPCClient) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
	if c == nil {
		return fmt.Errorf("rpc call %s: client is not configured", method)
	}
	if params == nil {
		params = []interface{}{}
	}
	set := c.endpoints.Load()
	if set == nil || len(set.members) == 0 {
		return fmt.Errorf("rpc call %s: no endpoint is configured", method)
	}
	return c.call(ctx, set, method, out, params)
}

// exchange posts one request to rawURL and reads the JSON-RPC result, with the kind
// of failure an error is (classify.go). retryRateLimits keeps the backoff loop on
// this endpoint for a rate-limited answer instead of returning it at once.
func (c *RPCClient) exchange(ctx context.Context, rawURL, method string, params []interface{}, retryRateLimits bool) (json.RawMessage, outcome, error) {
	for attempt := 1; ; attempt++ {
		status, header, respBody, kind, err := c.post(ctx, rawURL, method, params)
		if err != nil {
			return nil, kind, err
		}
		if isRateLimited(status, respBody) {
			if !retryRateLimits {
				return nil, outcomeRateLimited, fmt.Errorf("rpc call %s: %w (HTTP %d)", method, chain.ErrRateLimited, status)
			}
			if attempt >= c.retry.maxAttempts {
				return nil, outcomeRateLimited, fmt.Errorf("rpc call %s: %w (HTTP %d) after %d attempts", method, chain.ErrRateLimited, status, attempt)
			}
			delay := c.retry.delay(attempt, header.Get("Retry-After"), time.Now())
			slog.Warn("rpc rate limited, backing off", "chain", c.chainID, "method", method, "status", status, "attempt", attempt, "delay", delay.String())
			if err := c.retry.sleep(ctx, delay); err != nil {
				return nil, outcomeUnavailable, fmt.Errorf("rpc call %s: %w", method, err)
			}
			continue
		}
		return decodeRPCResponse(method, status, respBody)
	}
}

func (c *RPCClient) post(ctx context.Context, rawURL, method string, params []interface{}) (int, httpclient.Header, []byte, outcome, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.requestID.Add(1),
	})
	if err != nil {
		return 0, nil, nil, outcomeAnswer, fmt.Errorf("encode %s request: %w", method, err)
	}

	header := map[string]string{"Content-Type": "application/json", "User-Agent": rpcUserAgent}
	username, password := "", ""
	if rawURL == c.Endpoint() {
		for name, value := range c.headers {
			header[name] = value
		}
		username, password = c.username, c.password
	}
	limit := c.maxResponseBytes
	if limit <= 0 {
		limit = rpcMaxResponseBytes
	}
	resp, err := c.client.Do(ctx, httpclient.Request{
		Method:   httpclient.MethodPost,
		URL:      rawURL,
		Header:   header,
		Body:     body,
		HasBody:  true,
		Username: username,
		Password: password,
		MaxBytes: int64(limit) + 1,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, nil, nil, outcomeUnavailable, fmt.Errorf("build %s request: %w", method, withoutURL(err))
		}
		if httpclient.IsRead(err) {
			return 0, nil, nil, outcomeUnavailable, chain.Unavailable(fmt.Errorf("read %s response: %w", method, withoutURL(err)))
		}
		return 0, nil, nil, outcomeUnavailable, chain.Unavailable(fmt.Errorf("rpc call %s: %w", method, withoutURL(err)))
	}
	if len(resp.Body) > limit {
		return 0, nil, nil, outcomeAnswer, fmt.Errorf("rpc call %s: response larger than %d bytes", method, limit)
	}
	return resp.StatusCode, resp.Header, c.redactHeaderValues(resp.Body), outcomeAnswer, nil
}

// redactHeaderValues removes extra header values (API keys) from an answer
// before it can reach an error: a provider may echo an invalid key back.
func (c *RPCClient) redactHeaderValues(body []byte) []byte {
	if c == nil || len(c.headers) == 0 || len(body) == 0 {
		return body
	}
	for _, value := range c.headers {
		if value != "" {
			body = bytes.ReplaceAll(body, []byte(value), []byte("[redacted]"))
		}
	}
	return body
}

// decodeRPCResponse reads the result of one answer. A non-2xx status is the
// provider's HTTP failure and a JSON-RPC error is returned as *RPCError, each with
// the kind of failure it is.
func decodeRPCResponse(method string, status int, respBody []byte) (json.RawMessage, outcome, error) {
	if status < httpclient.StatusOK || status >= httpclient.StatusMultipleChoices {
		return nil, classifyHTTPFailure(status, respBody), chain.FromProviderHTTP(status, strings.TrimSpace(string(respBody)))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, outcomeUnavailable, fmt.Errorf("unmarshal %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return nil, classifyRPCError(rpcResp.Error), rpcResp.Error
	}
	return rpcResp.Result, outcomeAnswer, nil
}

// unmarshalResult stores a result into out. A nil out discards it.
func unmarshalResult(result json.RawMessage, out interface{}) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal(result, out)
}

// withoutURL drops the request URL from transport errors: provider URLs embed API keys.
func withoutURL(err error) error {
	return httpclient.WithoutURL(err)
}

// scrub removes endpoint from an error returned to callers. A JSON-RPC error
// stays on the unwrap chain so callers can still match its code. Error() is the
// typed sentinel, not the provider sentence. The URL is not logged.
func scrub(endpoint string, err error) error {
	if err == nil {
		return nil
	}
	var failure *chain.Failure
	if errors.As(err, &failure) && failure != nil {
		failure.Cause = httpclient.RedactURL(failure.Cause, endpoint)
		return err
	}
	if rpcErr, ok := err.(*rpcError); ok && rpcErr != nil {
		clone := *rpcErr
		clone.Message = httpclient.RedactURLText(rpcErr.Message, endpoint)
		return chain.Wrap(chain.KindOrProvider(0, clone.Message), &clone)
	}
	return httpclient.RedactURL(err, endpoint)
}
