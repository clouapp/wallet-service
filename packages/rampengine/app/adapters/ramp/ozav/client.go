// Package ozav is the Ozav provider adapter: an HTTP mapper between the ramp
// contract and Ozav's v2 API. It never touches the database; the service
// resolves a ramp.CustomerRef and passes it in.
package ozav

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
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// BaseURL serves both Ozav environments; the key prefix selects between them.
const BaseURL = "https://developers.ozav.io/v2"

// Key prefixes are how Ozav marks which environment a secret belongs to.
const (
	testKeyPrefix = "ozav_sk_test_"
	liveKeyPrefix = "ozav_sk_live_"
)

const defaultTimeout = 30 * time.Second

// idempotencyKeyPattern is Ozav's contract for Idempotency-Key. The service
// derives keys like "kyc-app:<id>" for every provider, and the colon is what
// Ozav rejects (measured: INVALID_IDEMPOTENCY_KEY, accepted with a hyphen).
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

var invalidIdempotencyKeyChar = regexp.MustCompile(`[^A-Za-z0-9_-]`)

const (
	minIdempotencyKeyLen = 16
	maxIdempotencyKeyLen = 256
)

// normalizeIdempotencyKey maps a key into Ozav's alphabet deterministically,
// since a retry must produce the same header. A valid key is returned as is.
// Otherwise every character outside the alphabet becomes '-'; a key over the
// ceiling is truncated from the front, because every derived key carries its
// discriminating id last; and a key under the floor is extended with a digest
// of the original, which keeps it deterministic and distinct. The floor is
// reachable: "kyc-link:<userID>:ozav" is 15 characters for a one-digit user,
// and refusing it failed every hosted link for user 3 (measured 28/08).
func normalizeIdempotencyKey(key string) string {
	if idempotencyKeyPattern.MatchString(key) {
		return key
	}

	normalized := invalidIdempotencyKeyChar.ReplaceAllString(key, "-")
	if len(normalized) > maxIdempotencyKeyLen {
		normalized = normalized[len(normalized)-maxIdempotencyKeyLen:]
	}
	if len(normalized) < minIdempotencyKeyLen {
		normalized = extendToFloor(key, normalized)
	}
	return normalized
}

// extendToFloor keeps the readable part in front ("kyc-link-3-ozav-a1b2…"), so
// a support ticket still says which call it was.
func extendToFloor(original, normalized string) string {
	sum := sha256.Sum256([]byte(original))
	return (normalized + "-" + hex.EncodeToString(sum[:]))[:minIdempotencyKeyLen+8]
}

// ValidateKeyMode refuses a live key in sandbox mode or the reverse: the host
// does not tell the environments apart, only the prefix does.
func ValidateKeyMode(apiKey, mode string) error {
	switch {
	case strings.HasPrefix(apiKey, testKeyPrefix):
		if mode != models.ProviderModeSandbox {
			return fmt.Errorf("ozav: test key cannot run in mode %q", mode)
		}
	case strings.HasPrefix(apiKey, liveKeyPrefix):
		if mode != models.ProviderModeProduction {
			return fmt.Errorf("ozav: live key cannot run in mode %q", mode)
		}
	default:
		return errors.New("ozav: api key has an unrecognized prefix")
	}
	return nil
}

// Config wires a Client.
type Config struct {
	APIKey  string
	Timeout time.Duration
	// BaseURL, HTTPClient and UploadHosts point a Client at a stub (tests,
	// cmd/devstub). Production leaves them empty: one host serves both
	// environments and the key prefix picks between them, so a stored base URL
	// could only ever be wrong.
	BaseURL string
	// HTTPClient is copied, never written to: the dev wiring shares one.
	HTTPClient *http.Client
	// UploadHosts replaces the production upload allow-list (see uploadHosts).
	UploadHosts []string
}

// uploadHosts is where Upload may send the API key besides the API host. A
// document upload PUTs to a URL taken from a response body, and Ozav requires
// the key on that PUT, so an unchecked host would hand the key to whoever can
// shape the declare response.
var uploadHosts = map[string]struct{}{
	"storage.ozav.io":    {},
	"developers.ozav.io": {},
}

// Request is one Ozav call.
type Request struct {
	Method string
	// Path never carries a query: LogUpstreamCall writes it at Info and
	// UpstreamError copies it into its message.
	Path  string
	Query url.Values
	Body  any
	// IdempotencyKey is derived by the service from a value that survives a
	// retry; the client normalizes it to Ozav's alphabet.
	IdempotencyKey string
}

// Client speaks to Ozav. It is safe for concurrent use.
type Client struct {
	cfg         Config
	baseURL     string
	http        *http.Client
	uploadHosts map[string]struct{}
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
	allowed := uploadHosts
	if len(cfg.UploadHosts) > 0 {
		allowed = make(map[string]struct{}, len(cfg.UploadHosts))
		for _, host := range cfg.UploadHosts {
			allowed[host] = struct{}{}
		}
	}
	return &Client{cfg: cfg, baseURL: cmp.Or(cfg.BaseURL, BaseURL), http: hc, uploadHosts: allowed}
}

// Upload PUTs the bytes of one document to the grant's upload_url, with the API
// key. The host is checked before anything is built: once the request is sent
// the key has left, and a refusal afterwards protects nothing.
func (c *Client) Upload(ctx context.Context, rawURL, mime string, body []byte, out any) error {
	started := time.Now()
	err := c.upload(ctx, rawURL, mime, body, out)
	ramp.LogUpstreamCall(ramp.ProviderOzav, "PUT upload_url", started, err)
	return err
}

func (c *Client) upload(ctx context.Context, rawURL, mime string, body []byte, out any) error {
	// The url is read out of Ozav's own answer, so one that does not parse is
	// its answer being unusable.
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ozav: parse upload url: %w: %w: %w",
			ramp.ErrProviderFailed, ramp.ErrRequestNotSent, err)
	}
	if _, ok := c.uploadHosts[parsed.Hostname()]; !ok || parsed.Scheme != "https" {
		return fmt.Errorf("ozav: upload host %q: %w: %w", parsed.Hostname(),
			ramp.ErrRequestNotSent, ramp.ErrUploadHostNotAllowed)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ozav: build upload request: %w: %w", ramp.ErrRequestNotSent, err)
	}
	httpReq.Header.Set("Content-Type", mime)
	return c.send(httpReq, parsed.Path, out)
}

// Do performs one call and decodes a 2xx body into out. Latency is logged for
// every call: a create cannot fail over once it is sent, so slow is the
// warning that matters.
func (c *Client) Do(ctx context.Context, req Request, out any) error {
	started := time.Now()
	err := c.do(ctx, req, out)
	ramp.LogUpstreamCall(ramp.ProviderOzav, req.Method+" "+req.Path, started, err)
	return err
}

func (c *Client) do(ctx context.Context, req Request, out any) error {
	var body io.Reader
	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("ozav: marshal body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	endpoint := c.baseURL + req.Path
	if len(req.Query) > 0 {
		endpoint += "?" + req.Query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, endpoint, body)
	if err != nil {
		return fmt.Errorf("ozav: build request: %w: %w", ramp.ErrRequestNotSent, err)
	}
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", normalizeIdempotencyKey(req.IdempotencyKey))
	}
	return c.send(httpReq, req.Path, out)
}

// send authenticates the request, sends it through ramp.Send and decodes a 2xx
// body into out.
func (c *Client) send(httpReq *http.Request, path string, out any) error {
	httpReq.Header.Set("X-API-Key", c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, body, err := ramp.Send(c.http, httpReq)
	if err != nil {
		return fmt.Errorf("ozav: %s %s: %w", httpReq.Method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(httpReq.Method, path, resp.StatusCode, body)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("ozav: %s %s: decode response: %w: %w", httpReq.Method, path, ramp.ErrProviderFailed, err)
	}
	return nil
}
