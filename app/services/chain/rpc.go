package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	rpcUserAgent        = "Macro-Wallets/0.1 RPC"
	rpcMaxResponseBytes = 1 << 20
	rpcHTTPTimeout      = 30 * time.Second
)

// ---------------------------------------------------------------------------
// RPCClient — generic JSON-RPC 2.0 client.
// BTC, ETH, SOL all speak JSON-RPC — only method names differ.
// ---------------------------------------------------------------------------

type RPCClient struct {
	url       string
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

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

func NewRPCClient(url, user, pass string) *RPCClient {
	return &RPCClient{
		url:      url,
		username: user,
		password: pass,
		client:   httpclient.NewClient(rpcHTTPTimeout),
		retry:    defaultRateLimitRetry(),
	}
}

// Call executes a JSON-RPC method and unmarshals result into `out`. A rate-limited
// request (HTTP 429/403 or JSON-RPC 429) was not served, so it is retried with
// backoff; every other failure is returned at once.
func (c *RPCClient) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
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
				return fmt.Errorf("rpc call %s: rate limited (HTTP %d) after %d attempts", method, status, attempt)
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
		URL:      c.url,
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
			return 0, nil, nil, fmt.Errorf("read %s response: %w", method, withoutURL(err))
		}
		return 0, nil, nil, fmt.Errorf("rpc call %s: %w", method, withoutURL(err))
	}
	return resp.StatusCode, resp.Header, resp.Body, nil
}

func decodeRPCResponse(method string, status int, respBody []byte, out interface{}) error {
	if status < httpclient.StatusOK || status >= httpclient.StatusMultipleChoices {
		return fmt.Errorf(
			"rpc call %s: HTTP %d: %s",
			method,
			status,
			strings.TrimSpace(string(respBody)),
		)
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
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
