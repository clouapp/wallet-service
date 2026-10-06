package tron

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	tronHTTPTimeout = 30 * time.Second
	// tronMaxResponseBytes bounds one node answer; a full mainnet block with its
	// transaction infos stays well below it.
	tronMaxResponseBytes = 64 << 20
	// tronErrorBodyBytes is how much of an unexpected answer an error quotes.
	tronErrorBodyBytes = 512
	// tronAPIKeyHeader carries the optional TronGrid key; it is never logged.
	tronAPIKeyHeader = "TRON-PRO-API-KEY"
	tronUserAgent    = "Macro-Wallets/0.1 TRON"
)

// tronStatusError is a non-2xx node answer that was not a rate limit.
type tronStatusError struct {
	path   string
	status int
	body   string
}

func (e *tronStatusError) Error() string {
	return fmt.Sprintf("tron POST %s: HTTP %d: %s", e.path, e.status, e.body)
}

// tronCallResult is the result object java-tron returns from contract calls and
// broadcasts; message is hex-encoded text.
type tronCallResult struct {
	Result  bool   `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r tronCallResult) describe() string {
	message := decodeTronMessage(r.Message)
	switch {
	case r.Code != "" && message != "":
		return r.Code + ": " + message
	case r.Code != "":
		return r.Code
	default:
		return message
	}
}

// decodeTronMessage renders java-tron's hex-encoded messages as text when they are.
func decodeTronMessage(message string) string {
	decoded, err := hex.DecodeString(message)
	if err != nil || !utf8.Valid(decoded) {
		return message
	}
	return string(decoded)
}

// post sends request as JSON to {rpc_url}{path} and decodes the answer into out.
// Rate-limited answers (HTTP 429/403) are retried with backoff. java-tron reports
// request errors as HTTP 200 with an "Error" member, which is returned as an error.
// Errors never carry the base URL or the API key.
func (a *TronLive) post(ctx context.Context, path string, request, out any) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("tron path %q must start with /", path)
	}
	if a.cfg.RPCURL == "" {
		return fmt.Errorf("tron %s: RPC URL is not configured", a.cfg.ChainIDStr)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode tron %s request: %w", path, err)
	}
	requestURL := strings.TrimRight(a.cfg.RPCURL, "/") + path
	for attempt := 1; ; attempt++ {
		status, header, respBody, err := a.fetch(ctx, requestURL, path, body)
		if err != nil {
			return err
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			return decodeTronResponse(path, respBody, out)
		}
		if !isRateLimited(status, respBody) {
			return &tronStatusError{path: path, status: status, body: truncateTronBody(respBody)}
		}
		if attempt >= a.retry.maxAttempts {
			return fmt.Errorf("tron POST %s: %w (HTTP %d) after %d attempts", path, chain.ErrRateLimited, status, attempt)
		}
		delay := a.retry.delay(attempt, header.Get("Retry-After"), time.Now())
		slog.Warn("tron node rate limited, backing off", "chain", a.cfg.ChainIDStr, "path", path, "status", status, "attempt", attempt, "delay", delay.String())
		if err := a.retry.sleep(ctx, delay); err != nil {
			return fmt.Errorf("tron POST %s: %w", path, err)
		}
	}
}

func (a *TronLive) fetch(ctx context.Context, requestURL, path string, body []byte) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("build tron POST %s: %w", path, withoutURL(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", tronUserAgent)
	if a.cfg.APIKey != "" {
		req.Header.Set(tronAPIKeyHeader, a.cfg.APIKey)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("tron POST %s: %w", path, withoutURL(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, tronMaxResponseBytes+1))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read tron POST %s: %w", path, withoutURL(err))
	}
	if len(respBody) > tronMaxResponseBytes {
		return 0, nil, nil, fmt.Errorf("tron POST %s: response larger than %d bytes", path, tronMaxResponseBytes)
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

func decodeTronResponse(path string, body []byte, out any) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fmt.Errorf("tron POST %s: empty response", path)
	}
	if trimmed[0] == '{' {
		var nodeError struct {
			Error string `json:"Error"`
		}
		if err := json.Unmarshal(trimmed, &nodeError); err == nil && nodeError.Error != "" {
			return fmt.Errorf("tron POST %s: node error: %s", path, nodeError.Error)
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(trimmed, out); err != nil {
		return fmt.Errorf("parse tron POST %s: %w", path, err)
	}
	return nil
}

func truncateTronBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > tronErrorBodyBytes {
		return text[:tronErrorBodyBytes] + "..."
	}
	return text
}
