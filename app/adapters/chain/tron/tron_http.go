package tron

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
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

// tronNodeError is java-tron's answer about a request it read and refused (HTTP 200
// with an "Error" member).
type tronNodeError struct {
	path    string
	message string
}

func (e *tronNodeError) Error() string {
	return fmt.Sprintf("tron POST %s: node error: %s", e.path, e.message)
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

// post sends request as JSON to {endpoint}{path} on the chain's endpoints in order
// (rpc.Endpoints: the primary, then each fallback of the chain's network) and
// decodes the answer into out. java-tron reports request errors as HTTP 200 with an
// "Error" member, which is returned as an error. Errors never carry a URL or the
// API key.
func (a *TronLive) post(ctx context.Context, path string, request, out any) error {
	raw, err := a.postRaw(ctx, path, request, false)
	if err != nil {
		return err
	}
	return decodeTronResult(path, raw, out)
}

// postRaw is post without decoding: the answer of the endpoint that served the call.
// A broadcast moves to the next endpoint only while the previous one certainly did
// not take the bytes.
func (a *TronLive) postRaw(ctx context.Context, path string, request any, broadcast bool) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("tron path %q must start with /", path)
	}
	if a.endpoints.Endpoint() == "" {
		return nil, fmt.Errorf("tron %s: RPC URL is not configured", a.cfg.ChainIDStr)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode tron %s request: %w", path, err)
	}
	var answer []byte
	err = a.endpoints.Do(ctx, broadcast, func(ctx context.Context, at rpc.Attempt) (rpc.Outcome, error) {
		raw, kind, exchangeErr := a.exchange(ctx, at, path, body)
		if exchangeErr == nil && broadcast {
			kind, exchangeErr = judgeTronBroadcast(raw)
		}
		if exchangeErr == nil {
			answer = raw
		}
		return kind, exchangeErr
	})
	if err != nil {
		return nil, err
	}
	return answer, nil
}

// exchange posts body to {at.URL}{path} and says what the answer means for
// failover. Rate-limited answers (HTTP 429/403) move on, except on the last
// endpoint, which retries them with backoff. A path the endpoint does not serve
// (HTTP 404/405) is unsupported there; any other non-2xx answer and a 2xx body that
// is not JSON are an endpoint that could not answer.
func (a *TronLive) exchange(ctx context.Context, at rpc.Attempt, path string, body []byte) ([]byte, rpc.Outcome, error) {
	requestURL := strings.TrimRight(at.URL, "/") + path
	for attempt := 1; ; attempt++ {
		resp, err := a.client.Do(ctx, a.request(requestURL, body, at.Primary))
		if err != nil {
			return nil, rpc.OutcomeUnavailable, fmt.Errorf("tron POST %s: %w", path, err)
		}
		if len(resp.Body) > tronMaxResponseBytes {
			return nil, rpc.OutcomeUnavailable, fmt.Errorf("tron POST %s: response larger than %d bytes", path, tronMaxResponseBytes)
		}
		status := resp.StatusCode
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			raw, kind, err := readTronAnswer(path, resp.Body)
			return raw, kind, err
		}
		if !isRateLimited(status, resp.Body) {
			statusErr := &tronStatusError{path: path, status: status, body: truncateTronBody(resp.Body)}
			if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
				return nil, rpc.OutcomeUnsupported, statusErr
			}
			return nil, rpc.OutcomeUnavailable, statusErr
		}
		if !at.Last || attempt >= a.retry.maxAttempts {
			return nil, rpc.OutcomeRateLimited, fmt.Errorf("tron POST %s: %w (HTTP %d) after %d attempts", path, chain.ErrRateLimited, status, attempt)
		}
		delay := a.retry.delay(attempt, resp.Header.Get("Retry-After"), time.Now())
		slog.Warn("tron node rate limited, backing off", "chain", a.cfg.ChainIDStr, "path", path, "status", status, "attempt", attempt, "delay", delay.String())
		if err := a.retry.sleep(ctx, delay); err != nil {
			return nil, rpc.OutcomeRateLimited, fmt.Errorf("tron POST %s: %w (HTTP %d), backoff cut: %w", path, chain.ErrRateLimited, status, err)
		}
	}
}

// request is one POST of body; the API key goes to the primary only, a fallback
// being another provider.
func (a *TronLive) request(requestURL string, body []byte, primary bool) httpclient.Request {
	header := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"User-Agent":   tronUserAgent,
	}
	if key := a.currentAPIKey(); primary && key != "" {
		header[tronAPIKeyHeader] = key
	}
	return httpclient.Request{
		Method:   httpclient.MethodPost,
		URL:      requestURL,
		Header:   header,
		Body:     body,
		HasBody:  true,
		MaxBytes: tronMaxResponseBytes + 1,
	}
}

// readTronAnswer checks a 2xx answer: a body that is not JSON is an endpoint that
// could not answer, an "Error" member is the node's answer about the request.
func readTronAnswer(path string, body []byte) ([]byte, rpc.Outcome, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, rpc.OutcomeUnavailable, fmt.Errorf("tron POST %s: empty response", path)
	}
	if !json.Valid(trimmed) {
		var probe any
		return nil, rpc.OutcomeUnavailable, fmt.Errorf("parse tron POST %s: %w", path, json.Unmarshal(trimmed, &probe))
	}
	if trimmed[0] == '{' {
		var nodeError struct {
			Error string `json:"Error"`
		}
		if err := json.Unmarshal(trimmed, &nodeError); err == nil && nodeError.Error != "" {
			return nil, rpc.OutcomeAnswer, &tronNodeError{path: path, message: nodeError.Error}
		}
	}
	return trimmed, rpc.OutcomeAnswer, nil
}

// decodeTronResult decodes an answer readTronAnswer accepted into out.
func decodeTronResult(path string, body []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
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
