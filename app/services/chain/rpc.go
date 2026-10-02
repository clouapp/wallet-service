package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	client    *http.Client
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
		client:   httpclient.New(rpcHTTPTimeout),
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
				return fmt.Errorf("rpc call %s: %w (HTTP %d) after %d attempts", method, ErrRateLimited, status, attempt)
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

func (c *RPCClient) post(ctx context.Context, method string, params []interface{}) (int, http.Header, []byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.requestID.Add(1),
	})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("encode %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("build %s request: %w", method, withoutURL(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", rpcUserAgent)
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("rpc call %s: %w", method, withoutURL(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, rpcMaxResponseBytes))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read %s response: %w", method, withoutURL(err))
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

func decodeRPCResponse(method string, status int, respBody []byte, out interface{}) error {
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
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
