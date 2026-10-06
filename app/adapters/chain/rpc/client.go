package rpc

import (
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
type RPCClient struct {
	endpoint  atomic.Value // string; replaced without logging the URL
	client    *httpclient.Client
	requestID atomic.Uint64
	username  string
	password  string
	retry     rateLimitRetry
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

// RPCClientDeps is the endpoint and optional basic-auth pair NewRPCClient stores.
// Empty User and Password send no basic auth. The URL is not logged.
type RPCClientDeps struct {
	URL      string
	User     string
	Password string
}

func NewRPCClient(deps RPCClientDeps) *RPCClient {
	client := &RPCClient{
		username: deps.User,
		password: deps.Password,
		client:   httpclient.NewClient(rpcHTTPTimeout),
		retry:    defaultRateLimitRetry(),
	}
	client.endpoint.Store(deps.URL)
	return client
}

// Endpoint is the URL the next call dials. Callers must not log it.
func (c *RPCClient) Endpoint() string {
	if c == nil {
		return ""
	}
	value, _ := c.endpoint.Load().(string)
	return value
}

// ReplaceEndpoint points later calls at endpoint. An empty value is ignored
// so a failed read cannot wipe the current endpoint. The URL is not logged.
func (c *RPCClient) ReplaceEndpoint(endpoint string) {
	if c == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	c.endpoint.Store(endpoint)
}

// Call executes a JSON-RPC method and unmarshals result into `out`. A rate-limited
// request (HTTP 429/403 or JSON-RPC 429) was not served, so it is retried with
// backoff; every other failure is returned at once.
func (c *RPCClient) Call(ctx context.Context, method string, out interface{}, params ...interface{}) (err error) {
	defer func() { err = c.scrub(err) }()
	if params == nil {
		params = []interface{}{}
	}

	for attempt := 1; ; attempt++ {
		status, header, respBody, err := c.post(ctx, method, params)
		if err != nil {
			return err
		}
		if isRateLimited(status, respBody) {
			if attempt >= c.retry.maxAttempts {
				return fmt.Errorf("rpc call %s: %w (HTTP %d) after %d attempts", method, chain.ErrRateLimited, status, attempt)
			}
			delay := c.retry.delay(attempt, header.Get("Retry-After"), time.Now())
			slog.Warn("rpc rate limited, backing off", "method", method, "status", status, "attempt", attempt, "delay", delay.String())
			if err := c.retry.sleep(ctx, delay); err != nil {
				return fmt.Errorf("rpc call %s: %w", method, err)
			}
			continue
		}
		return decodeRPCResponse(method, status, respBody, out)
	}
}

func (c *RPCClient) post(ctx context.Context, method string, params []interface{}) (int, httpclient.Header, []byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.requestID.Add(1),
	})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("encode %s request: %w", method, err)
	}

	resp, err := c.client.Do(ctx, httpclient.Request{
		Method:   httpclient.MethodPost,
		URL:      c.Endpoint(),
		Header:   map[string]string{"Content-Type": "application/json", "User-Agent": rpcUserAgent},
		Body:     body,
		HasBody:  true,
		Username: c.username,
		Password: c.password,
		MaxBytes: rpcMaxResponseBytes,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, nil, nil, fmt.Errorf("build %s request: %w", method, withoutURL(err))
		}
		if httpclient.IsRead(err) {
			return 0, nil, nil, chain.Unavailable(fmt.Errorf("read %s response: %w", method, withoutURL(err)))
		}
		return 0, nil, nil, chain.Unavailable(fmt.Errorf("rpc call %s: %w", method, withoutURL(err)))
	}
	return resp.StatusCode, resp.Header, resp.Body, nil
}

func decodeRPCResponse(method string, status int, respBody []byte, out interface{}) error {
	if status < httpclient.StatusOK || status >= httpclient.StatusMultipleChoices {
		return chain.FromProviderHTTP(status, strings.TrimSpace(string(respBody)))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("unmarshal %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if out != nil {
		return json.Unmarshal(rpcResp.Result, out)
	}
	return nil
}

// withoutURL drops the request URL from transport errors: provider URLs embed API keys.
func withoutURL(err error) error {
	return httpclient.WithoutURL(err)
}

// scrub removes the endpoint from an error returned to callers. A JSON-RPC error
// stays on the unwrap chain so callers can still match its code. Error() is the
// typed sentinel, not the provider sentence. The URL is not logged.
func (c *RPCClient) scrub(err error) error {
	if err == nil || c == nil {
		return err
	}
	endpoint := c.Endpoint()
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
