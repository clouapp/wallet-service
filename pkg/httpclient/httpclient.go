// Package httpclient builds the HTTP clients used for long-lived connections to chain
// providers (RPC nodes, Esplora, block explorers).
package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// A provider connection that sends no frame for this long gets an HTTP/2 PING.
	pingAfterIdleRead = 15 * time.Second
	// A PING left unanswered this long closes the connection.
	pingTimeout = 10 * time.Second
)

var sharedTransport = newTransport(pingAfterIdleRead, pingTimeout)

// New returns a client with the given total request timeout over a transport whose
// HTTP/2 connections are health-checked. Without the check, a connection that dies
// silently (network change, NAT drop, Wi-Fi switch) keeps receiving every request until
// the kernel gives up retransmitting, which takes 15 to 30 minutes; each request in
// between only ends at the client timeout.
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		panic(fmt.Sprintf("httpclient: timeout must be positive, got %s", timeout))
	}
	return &http.Client{Timeout: timeout, Transport: sharedTransport}
}

func newTransport(pingAfter, pingWait time.Duration) *http.Transport {
	transport := baseTransport()
	transport.HTTP2 = &http.HTTP2Config{
		SendPingTimeout: pingAfter,
		PingTimeout:     pingWait,
	}
	return transport
}

func baseTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
}

// Status and method values services compare without importing net/http.
const (
	MethodGet    = http.MethodGet
	MethodPost   = http.MethodPost
	MethodPut    = http.MethodPut
	MethodPatch  = http.MethodPatch
	MethodDelete = http.MethodDelete

	StatusOK              = http.StatusOK
	StatusCreated         = http.StatusCreated
	StatusNoContent       = http.StatusNoContent
	StatusMultipleChoices = http.StatusMultipleChoices
	StatusBadRequest      = http.StatusBadRequest
	StatusForbidden       = http.StatusForbidden
	StatusNotFound        = http.StatusNotFound
	StatusTooManyRequests = http.StatusTooManyRequests
)

// Client performs outbound HTTP calls. Callers never see net/http types.
type Client struct {
	raw *http.Client
}

// NewClient returns a Client with the given total request timeout.
func NewClient(timeout time.Duration) *Client {
	return Wrap(New(timeout))
}

// Wrap adapts an existing client, including one from httptest.Server.Client.
func Wrap(raw *http.Client) *Client {
	if raw == nil {
		panic("httpclient: client is required")
	}
	return &Client{raw: raw}
}

// Header is a response header map. Get matches names case-insensitively.
type Header map[string][]string

// Get returns the first value for key, or empty when absent.
func (h Header) Get(key string) string {
	return http.Header(h).Get(key)
}

// Request is one outbound call.
type Request struct {
	Method   string
	URL      string
	Header   map[string]string
	Body     []byte
	HasBody  bool
	Username string
	Password string
	// MaxBytes limits the response body. Zero reads the whole body.
	MaxBytes int64
}

// Response is the status, headers, and body of one call.
type Response struct {
	StatusCode int
	Header     Header
	Body       []byte
}

// Do sends call and reads its body. A nil context, empty method, or empty URL fails
// before any network use. Transport errors stay unwrapped so callers can drop the URL.
func (c *Client) Do(ctx context.Context, call Request) (Response, error) {
	if c == nil || c.raw == nil {
		return Response{}, fmt.Errorf("httpclient: client is required")
	}
	if ctx == nil {
		return Response{}, phase("build", fmt.Errorf("httpclient: context is required"))
	}
	if call.Method == "" {
		return Response{}, phase("build", fmt.Errorf("httpclient: method is required"))
	}
	if call.URL == "" {
		return Response{}, phase("build", fmt.Errorf("httpclient: url is required"))
	}

	var body io.Reader
	if call.HasBody {
		body = bytes.NewReader(call.Body)
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, call.URL, body)
	if err != nil {
		return Response{}, phase("build", err)
	}
	for key, value := range call.Header {
		req.Header.Set(key, value)
	}
	if call.Username != "" {
		req.SetBasicAuth(call.Username, call.Password)
	}

	resp, err := c.raw.Do(req)
	if err != nil {
		return Response{}, phase("roundtrip", err)
	}
	defer resp.Body.Close()

	reader := io.Reader(resp.Body)
	if call.MaxBytes > 0 {
		reader = io.LimitReader(resp.Body, call.MaxBytes)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return Response{}, phase("read", err)
	}
	return Response{
		StatusCode: resp.StatusCode,
		Header:     cloneHeader(resp.Header),
		Body:       payload,
	}, nil
}

// ParseTime reads an HTTP-date, as Retry-After may send one.
func ParseTime(value string) (time.Time, error) {
	return http.ParseTime(value)
}

// IsBuild reports a failure while constructing the request.
func IsBuild(err error) bool {
	return phaseOf(err) == "build"
}

// IsRead reports a failure while reading the response body.
func IsRead(err error) bool {
	return phaseOf(err) == "read"
}

type phaseError struct {
	phase string
	err   error
}

func phase(name string, err error) error {
	return &phaseError{phase: name, err: err}
}

func (e *phaseError) Error() string {
	if e == nil || e.err == nil {
		return "httpclient: request failed"
	}
	return e.err.Error()
}

func (e *phaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func phaseOf(err error) string {
	var phased *phaseError
	if errors.As(err, &phased) {
		return phased.phase
	}
	return ""
}

func cloneHeader(header http.Header) Header {
	if len(header) == 0 {
		return Header{}
	}
	cloned := make(Header, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
