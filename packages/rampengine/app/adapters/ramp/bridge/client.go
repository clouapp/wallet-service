// Package bridge is the Bridge provider adapter: an HTTP mapper between the
// ramp contract and Bridge's REST API. It never touches the database; the
// service resolves a ramp.CustomerRef and passes it in.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Base URLs are adapter constants rather than settings: a wrong value in the
// database would break authentication silently.
const (
	SandboxBaseURL    = "https://api.sandbox.bridge.xyz/v0"
	ProductionBaseURL = "https://api.bridge.xyz/v0"
)

// defaultTimeout is longer than the API's own request timeout on purpose: the
// KYC submit runs on a queue worker, not inside an HTTP handler.
const defaultTimeout = 30 * time.Second

// Config wires a Client.
type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
	// HTTPClient replaces the default transport, for cmd/devstub's self-signed
	// certificate. It is copied, never written to: the dev wiring shares one
	// across every factory call.
	HTTPClient *http.Client
}

// BaseURLForMode maps a provider_configs.mode onto the matching Bridge host.
func BaseURLForMode(mode string) (string, error) {
	switch mode {
	case models.ProviderModeSandbox:
		return SandboxBaseURL, nil
	case models.ProviderModeProduction:
		return ProductionBaseURL, nil
	default:
		return "", fmt.Errorf("bridge: unknown provider mode %q", mode)
	}
}

// Request is one Bridge call.
type Request struct {
	Method string
	// Path never carries a query: LogUpstreamCall writes it at Info and
	// UpstreamError copies it into its message, so a value from the request (an
	// e-mail) would be logged with it.
	Path  string
	Query url.Values
	Body  any
	// IdempotencyKey is derived by the service from a value that survives a
	// retry; the client only forwards it.
	IdempotencyKey string
}

// Client speaks to one Bridge host. It is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient returns a Client for cfg, with defaultTimeout when cfg names none.
func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	hc := &http.Client{Timeout: cfg.Timeout}
	if cfg.HTTPClient != nil {
		copied := *cfg.HTTPClient
		if copied.Timeout == 0 {
			copied.Timeout = cfg.Timeout
		}
		hc = &copied
	}
	return &Client{cfg: cfg, http: hc}
}

// Do performs one call and decodes a 2xx body into out. Latency is logged for
// every call: a create cannot fail over once it is sent, so slow is the
// warning that matters.
func (c *Client) Do(ctx context.Context, req Request, out any) error {
	started := time.Now()
	err := c.do(ctx, req, out)
	ramp.LogUpstreamCall(ramp.ProviderBridge, req.Method+" "+req.Path, started, err)
	return err
}

func (c *Client) do(ctx context.Context, req Request, out any) error {
	var body io.Reader
	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("bridge: marshal body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	endpoint := c.cfg.BaseURL + req.Path
	if len(req.Query) > 0 {
		endpoint += "?" + req.Query.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, endpoint, body)
	if err != nil {
		return fmt.Errorf("bridge: build request: %w: %w", ramp.ErrRequestNotSent, err)
	}
	httpReq.Header.Set("Api-Key", c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	resp, respBody, err := ramp.Send(c.http, httpReq)
	if err != nil {
		return fmt.Errorf("bridge: %s %s: %w", req.Method, req.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(req, resp.StatusCode, respBody)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("bridge: %s %s: decode response: %w: %w", req.Method, req.Path, ramp.ErrProviderFailed, err)
	}
	return nil
}

// UpstreamError is a structured rejection from Bridge. It carries the code
// callers branch on and never Message or a body value: Bridge echoes the
// submitted customer fields, PII included, in its validation errors.
type UpstreamError struct {
	Method, Path, Code string
	// Status is kept because some rejections (a refused key, a throttle) are
	// identifiable by status alone.
	Status int
	// BodyKeys names the top-level fields of a rejection that carried no code,
	// so the log says what Bridge sent instead. Names only, never values.
	BodyKeys []string
	// Fields names the request fields a rejection pointed at (source.key),
	// sorted, so the admin reads which one Bridge refused. Names only: the
	// reason beside each may restate a value.
	Fields []string
}

func (e *UpstreamError) Error() string {
	if len(e.BodyKeys) > 0 {
		return fmt.Sprintf("bridge: %s %s: upstream rejected (%s; body keys: %s)",
			e.Method, e.Path, e.Code, strings.Join(e.BodyKeys, ","))
	}
	if len(e.Fields) > 0 {
		return fmt.Sprintf("bridge: %s %s: upstream rejected (%s: %s)",
			e.Method, e.Path, e.Code, strings.Join(e.Fields, ", "))
	}
	return fmt.Sprintf("bridge: %s %s: upstream rejected (%s)", e.Method, e.Path, e.Code)
}

// Unwrap names the domain sentinel a rejection stands for, so
// resources.ErrorCode can answer something better than provider_unavailable.
// Only rejections measured against Bridge are mapped: inventing a vocabulary
// is how a fault on this side starts being reported as the user's.
func (e *UpstreamError) Unwrap() error { return sentinelForUpstream(e.Code, e.Status) }

// Is makes every rejection a provider failure on top of whatever sentinel
// Unwrap names, so a code the table does not know still answers 502.
func (e *UpstreamError) Is(target error) bool { return target == ramp.ErrProviderFailed }

func sentinelForUpstream(code string, status int) error {
	switch code {
	// Measured 01/09 on POST /customers/{id}/external_accounts: Bridge refuses
	// a payout destination whose rail the customer's endorsements do not
	// cover, and XIP's snapshot of them can lag (a delayed webhook, a
	// revocation upstream), so a local KYC row may still say approved.
	case "missing_required_endorsements":
		return ramp.ErrKycPending
	// A 401 that names not_allowed is a rail or feature the account is not
	// enabled for, not a refused key (transfer-errors-and-recovery, checked
	// 2026-10-14). Matched before the status, which would read it as one.
	case "not_allowed":
		return ramp.ErrProviderFeatureDisabled
	}
	switch status {
	// A key Bridge refuses is the one failure an admin fixes without a deploy.
	case http.StatusUnauthorized:
		return ramp.ErrProviderCredentialsInvalid
	// A throttle that carried a body reached Bridge, so it is not
	// ErrRequestNotSent and no other provider may serve it, but the same
	// request to Bridge is expected to work later.
	case http.StatusTooManyRequests:
		return ramp.ErrRetryableUpstream
	}
	return nil
}

// classify turns a non-2xx into an error the caller can branch on, never
// including the body verbatim (see UpstreamError).
func classify(req Request, status int, body []byte) error {
	// A throttle with no body is the one post-send answer that is provably
	// inert: Bridge refused before doing any work, so failing over cannot
	// duplicate anything.
	if status == http.StatusTooManyRequests && len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("bridge: %s %s: throttled: %w: %w",
			req.Method, req.Path, ramp.ErrProviderFailed, ramp.ErrRequestNotSent)
	}

	var decoded errorResponse
	code := ""
	var fields []string
	if err := json.Unmarshal(body, &decoded); err == nil {
		code = normalizeUpstreamCode(decoded)
		if decoded.Source != nil {
			fields = decoded.Source.fieldNames()
		}
	} else {
		// One field of an unexpected type fails the whole decode, and with it
		// the code, which is the one thing callers branch on.
		var codeOnly struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(body, &codeOnly) == nil {
			code = codeOnly.Code
		}
	}
	if code == "" {
		return &UpstreamError{
			Method: req.Method, Path: req.Path, Code: fmt.Sprintf("http_%d", status), Status: status,
			BodyKeys: topLevelKeys(body),
		}
	}
	return &UpstreamError{Method: req.Method, Path: req.Path, Code: code, Status: status, Fields: fields}
}

// topLevelKeys lists a JSON object's field names, sorted, or nil for anything
// that is not an object.
func topLevelKeys(body []byte) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(fields))
}

// normalizeUpstreamCode maps Bridge's envelope onto the codes callers branch on.
func normalizeUpstreamCode(decoded errorResponse) string {
	code := decoded.Code
	if code == "invalid_parameters" && isDuplicateEmailParameter(decoded) {
		return "duplicate_email"
	}
	return code
}

// isDuplicateEmailParameter prefers Bridge's structured source.key; Message is
// a fallback when the sandbox omits source (see kyc_adapter_test duplicate-email
// fixtures).
func isDuplicateEmailParameter(decoded errorResponse) bool {
	if decoded.Source != nil && decoded.Source.namesDuplicateEmail() {
		return true
	}
	return duplicateEmailMessage(decoded.Message)
}

func duplicateEmailMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "email") &&
		(strings.Contains(lower, "already exists") ||
			strings.Contains(lower, "duplicate") ||
			strings.Contains(lower, "already in use"))
}

// errorSource is where a rejection points. Its key is the field's name
// ("email"), or an object naming each field and why
// ({"email": "A customer with this email already exists"}, the sandbox's
// answer to a duplicate e-mail on 18/10/2026); a string field would fail the
// whole envelope on the second shape.
type errorSource struct {
	Key json.RawMessage `json:"key"`
}

// fieldNames is the fields the source points at, sorted: its key, or each
// name of its object of reasons.
func (s errorSource) fieldNames() []string {
	var name string
	if json.Unmarshal(s.Key, &name) == nil {
		if name == "" {
			return nil
		}
		return []string{name}
	}
	var reasons map[string]json.RawMessage
	if json.Unmarshal(s.Key, &reasons) != nil || len(reasons) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(reasons))
}

// namesDuplicateEmail reports whether the source points at the e-mail: by
// name, or by a reason that says it is taken.
func (s errorSource) namesDuplicateEmail() bool {
	var name string
	if json.Unmarshal(s.Key, &name) == nil {
		return strings.EqualFold(name, "email")
	}
	var reasons map[string]string
	if json.Unmarshal(s.Key, &reasons) != nil {
		return false
	}
	for field, reason := range reasons {
		if strings.EqualFold(field, "email") && duplicateEmailMessage("email "+reason) {
			return true
		}
	}
	return false
}

// errorResponse is the envelope Bridge returns on failure.
type errorResponse struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Source  *errorSource `json:"source"`
}
