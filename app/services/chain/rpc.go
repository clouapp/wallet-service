package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
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
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Call executes a JSON-RPC method and unmarshals result into `out`.
func (c *RPCClient) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
	if params == nil {
		params = []interface{}{}
	}

	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.requestID.Add(1),
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("rpc call %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

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
