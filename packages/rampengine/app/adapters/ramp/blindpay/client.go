package blindpay

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// BaseURL serves both BlindPay environments; the instance the API key belongs
// to decides which one a request hits.
const BaseURL = "https://api.blindpay.com/v1"

const defaultTimeout = 30 * time.Second

// maxIdempotencyKeyLen is BlindPay's cap on Idempotency-Key.
const maxIdempotencyKeyLen = 255

// Scope is the URL prefix a request lives under. Almost everything is under
// the instance; the hosted terms-of-service session is under /e/, BlindPay's
// "external" prefix.
type Scope int

const (
	ScopeInstance Scope = iota
	ScopeExternal
)

// Config wires a Client.
type Config struct {
	APIKey     string
	InstanceID string
	// Mode is the provider's admin mode (sandbox or production).
	Mode string
	// Mints is the Solana mint and decimals each stablecoin settles in, keyed
	// by the client's token (USDC, USDT). A development instance settles both
	// in USDB, so the admin names its mint under each. A token absent here
	// cannot be paid out.
	Mints map[string]MintConfig
	// Delegate is the address the prepared funding delegates the stablecoin
	// to (solana_delegate_address).
	Delegate string
	// SolanaRPCURL is the node a signed delegation is sent through before the
	// payout is created (solana_rpc_url). Empty leaves the sending to the
	// provider; the payout gate keeps a real payout from running without it.
	SolanaRPCURL string
	Timeout      time.Duration
	// BaseURL and HTTPClient point a Client at a stub (tests, local dev).
	// Production leaves both empty.
	BaseURL string
	// HTTPClient is copied, never written to.
	HTTPClient *http.Client
}

// MintConfig is one stablecoin's Solana mint, as base58, and its decimals.
type MintConfig struct {
	Address  string
	Decimals uint8
}

// Request is one BlindPay call.
type Request struct {
	Method string
	// Path is relative to the instance ("customers", "customers/re_1/rfi").
	// The client prefixes it and escapes each segment, so a caller can never
	// name a path outside the instance. It never carries a query:
	// LogUpstreamCall writes it at Info.
	Path  string
	Query url.Values
	Body  any
	// RawBody is a body already encoded, sent byte for byte as JSON. It is
	// for a create whose retries must carry the exact bytes of the first
	// attempt (BlindPay compares a replayed Idempotency-Key by the hash of
	// the body), which re-encoding a value cannot promise. Body must be nil
	// alongside it.
	RawBody []byte
	// IdempotencyKey is derived by the service from a value that survives a
	// retry.
	IdempotencyKey string
	Scope          Scope
	// Quiet keeps the provider's message out of the error a refusal becomes:
	// the request carries personal data and the answer may echo it.
	Quiet bool
}

// Client speaks to BlindPay. It is safe for concurrent use.
type Client struct {
	cfg     Config
	baseURL string
	http    *http.Client
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
	return &Client{cfg: cfg, baseURL: strings.TrimRight(cmp.Or(cfg.BaseURL, BaseURL), "/"), http: hc}
}

// Mode is the provider mode the client was built for.
func (c *Client) Mode() string { return c.cfg.Mode }

// Mint is the mint the client's token settles in, if one is configured.
func (c *Client) Mint(token string) (MintConfig, bool) {
	mint, ok := c.cfg.Mints[strings.ToUpper(strings.TrimSpace(token))]
	return mint, ok && mint.Address != ""
}

// Delegate is the address the prepared funding must delegate to.
func (c *Client) Delegate() string { return c.cfg.Delegate }

// SolanaRPCURL is the node the signed delegation is sent through, or empty.
func (c *Client) SolanaRPCURL() string { return c.cfg.SolanaRPCURL }

// WithSolanaRPCURL is a copy of the client that sends delegations through the
// node. It exists for tests that build a client over a stub.
func (c *Client) WithSolanaRPCURL(url string) *Client {
	copied := *c
	copied.cfg.SolanaRPCURL = url
	return &copied
}

// InstanceID is the instance every request is scoped to.
func (c *Client) InstanceID() string { return c.cfg.InstanceID }

// Do performs one call and decodes a 2xx body into out. Latency is logged for
// every call.
func (c *Client) Do(ctx context.Context, req Request, out any) error {
	started := time.Now()
	err := c.do(ctx, req, out)
	ramp.LogUpstreamCall(ramp.ProviderBlindPay, req.Method+" "+req.Path, started, err)
	return err
}

// errUndecodable marks a 2xx answer whose body does not decode: the call
// took effect, but its answer cannot be read.
var errUndecodable = errors.New("undecodable answer")

func (c *Client) do(ctx context.Context, req Request, out any) error {
	path, err := c.scopedPath(req.Scope, req.Path)
	if err != nil {
		return err
	}
	if req.Body != nil && req.RawBody != nil {
		return fmt.Errorf("blindpay: both Body and RawBody set: %w", ramp.ErrRequestNotSent)
	}
	var body io.Reader
	if req.RawBody != nil {
		body = bytes.NewReader(req.RawBody)
	}
	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("blindpay: marshal body: %w: %w", ramp.ErrRequestNotSent, err)
		}
		body = bytes.NewReader(encoded)
	}

	endpoint := c.baseURL + path
	if len(req.Query) > 0 {
		endpoint += "?" + req.Query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, endpoint, body)
	if err != nil {
		return fmt.Errorf("blindpay: build request: %w: %w", ramp.ErrRequestNotSent, err)
	}
	if req.Body != nil || req.RawBody != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", idempotencyKey(req.IdempotencyKey))
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, respBody, err := ramp.Send(c.http, httpReq)
	if err != nil {
		return fmt.Errorf("blindpay: %s %s: %w", req.Method, req.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(req.Method, req.Path, resp.StatusCode, respBody, req.Quiet)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("blindpay: %s %s: decode response: %w: %w: %w", req.Method, req.Path, ramp.ErrProviderFailed, errUndecodable, err)
	}
	return nil
}

// scopedPath prefixes a relative path with its scope's instance prefix,
// escaping every segment. A segment that could change what the path names —
// empty, "." or "..", or one carrying a separator, a query or a fragment — is
// refused before anything is sent: the request carries the API key, and a
// path built from a provider id or a user's input must not be able to address
// another resource with it.
func (c *Client) scopedPath(scope Scope, relative string) (string, error) {
	prefix := "/instances/"
	if scope == ScopeExternal {
		prefix = "/e/instances/"
	}
	segments := strings.Split(relative, "/")
	escaped := make([]string, 0, len(segments)+1)
	escaped = append(escaped, url.PathEscape(c.cfg.InstanceID))
	for _, segment := range append([]string{c.cfg.InstanceID}, segments...) {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\?#`) {
			return "", fmt.Errorf("blindpay: path %q: %w", relative, ramp.ErrRequestNotSent)
		}
	}
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	return prefix + strings.Join(escaped, "/"), nil
}

// idempotencyKey keeps a key within BlindPay's cap. An overlong key becomes a
// digest of itself rather than a truncation, so two long keys that share a
// prefix stay apart, and a retry still sends the same header.
func idempotencyKey(key string) string {
	if len(key) <= maxIdempotencyKeyLen {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256-" + hex.EncodeToString(sum[:])
}

// Upload sends one file to BlindPay's upload endpoint and answers the URL the
// customer payload must name it by.
//
// The endpoint is not under /instances/{id}/ — the instance travels in the
// query — and BlindPay applies no idempotency to multipart requests, so a
// retry uploads the file again. Callers that must not orphan uploads persist
// each returned URL before the next call (see the atomic KYC checkpoint).
func (c *Client) Upload(ctx context.Context, bucket, filename, mime string, data []byte) (string, error) {
	started := time.Now()
	fileURL, err := c.upload(ctx, bucket, filename, mime, data)
	ramp.LogUpstreamCall(ramp.ProviderBlindPay, "POST upload", started, err)
	return fileURL, err
}

func (c *Client) upload(ctx context.Context, bucket, filename, mime string, data []byte) (string, error) {
	id := c.cfg.InstanceID
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\?#`) {
		return "", fmt.Errorf("blindpay: upload instance: %w", ramp.ErrRequestNotSent)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("bucket", bucket); err != nil {
		return "", fmt.Errorf("blindpay: build upload: %w: %w", ramp.ErrRequestNotSent, err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", mime)
	part, err := form.CreatePart(header)
	if err == nil {
		_, err = part.Write(data)
	}
	if err == nil {
		err = form.Close()
	}
	if err != nil {
		return "", fmt.Errorf("blindpay: build upload: %w: %w", ramp.ErrRequestNotSent, err)
	}

	endpoint := c.baseURL + "/upload?" + url.Values{"instance_id": {id}}.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", fmt.Errorf("blindpay: build upload: %w: %w", ramp.ErrRequestNotSent, err)
	}
	httpReq.Header.Set("Content-Type", form.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, respBody, err := ramp.Send(c.http, httpReq)
	if err != nil {
		return "", fmt.Errorf("blindpay: POST upload: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", classify(http.MethodPost, "upload", resp.StatusCode, respBody, false)
	}
	var out struct {
		FileURL string `json:"file_url"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("blindpay: POST upload: decode: %w: %w", ramp.ErrProviderFailed, err)
	}
	parsed, err := url.Parse(out.FileURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("blindpay: POST upload: answer is not an https URL: %w", ramp.ErrProviderFailed)
	}
	return out.FileURL, nil
}
