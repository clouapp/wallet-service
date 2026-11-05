package postal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// maxResponseBytes bounds what a lookup reads: a CEP's answer is a few
// hundred bytes, and a provider answering far more is refused rather than
// read whole into memory.
const maxResponseBytes = 64 << 10

// Client fetches one provider's decoded answer for a postal code.
// Implementations must be safe for concurrent use.
type Client[T any] interface {
	Get(ctx context.Context, postalCode string) (T, error)
}

// NoNotFoundStatus is the notFoundStatus of a provider that reports an unknown
// postal code in the body rather than in the status line — ViaCEP answers 200
// with `erro` set, and its mapping is what decides.
const NoNotFoundStatus = 0

// NewHTTPClient is the Client every JSON-over-HTTP lookup provider shares.
//
// provider prefixes the errors, so an operator reading a failure knows which
// one answered; url builds the request from the postal code; notFoundStatus is
// the status that means a definitive not-found, or NoNotFoundStatus. No error
// names the postal code: they reach the application log, and
// .ai/guidelines/errors-and-logging.md keeps an address out of it.
func NewHTTPClient[T any](provider ProviderCode, url func(postalCode string) string, notFoundStatus int, timeout time.Duration) Client[T] {
	return &httpClient[T]{
		provider:       provider,
		url:            url,
		notFoundStatus: notFoundStatus,
		http:           &http.Client{Timeout: timeout},
	}
}

type httpClient[T any] struct {
	provider       ProviderCode
	url            func(postalCode string) string
	notFoundStatus int
	http           *http.Client
}

func (c *httpClient[T]) Get(ctx context.Context, postalCode string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(postalCode), nil)
	if err != nil {
		return zero, fmt.Errorf("%s: build request: %w", c.provider, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Do's *url.Error prints the request URL, and the postal code is in
		// its path; the cause alone says what failed.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return zero, fmt.Errorf("%s: lookup: %w: %w", c.provider, ErrProviderFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return zero, fmt.Errorf("%s: lookup: read body: %w: %w", c.provider, ErrProviderFailed, err)
	}
	if len(body) > maxResponseBytes {
		return zero, fmt.Errorf("%s: lookup: answer over %d bytes: %w", c.provider, maxResponseBytes, ErrProviderFailed)
	}
	if c.notFoundStatus != NoNotFoundStatus && resp.StatusCode == c.notFoundStatus {
		return zero, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("%s: lookup: unexpected status %d: %w", c.provider, resp.StatusCode, ErrProviderFailed)
	}

	var decoded T
	if err := json.Unmarshal(body, &decoded); err != nil {
		return zero, fmt.Errorf("%s: lookup: decode response: %w: %w", c.provider, ErrProviderFailed, err)
	}
	return decoded, nil
}

// JSONLookup is the LookupAdapter both providers are: a swappable client and
// that provider's own mapping from its decoded answer to the canonical Address.
type JSONLookup[T any] struct {
	mu      sync.RWMutex
	client  Client[T]
	code    ProviderCode
	address func(postalCode string, decoded T) (Address, error)
}

// NewJSONLookup wires a provider's client and mapping into a LookupAdapter.
// address answers ErrNotFound for a provider that reports an unknown postal
// code in the body it decoded.
func NewJSONLookup[T any](code ProviderCode, client Client[T], address func(postalCode string, decoded T) (Address, error)) *JSONLookup[T] {
	return &JSONLookup[T]{client: client, code: code, address: address}
}

func (l *JSONLookup[T]) Code() ProviderCode { return l.code }

// SetClient swaps the transport at runtime and returns a restore function.
//
// Integration tests only; production code must never call it. Routes are wired
// at boot holding this adapter's pointer, so a container rebind would not reach
// them — the same trick as bridge.Adapter.SetFactories.
func (l *JSONLookup[T]) SetClient(client Client[T]) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	previous := l.client
	l.client = client
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.client = previous
	}
}

// Lookup resolves postalCode against the provider.
func (l *JSONLookup[T]) Lookup(ctx context.Context, postalCode string) (Address, error) {
	l.mu.RLock()
	client := l.client
	l.mu.RUnlock()

	decoded, err := client.Get(ctx, postalCode)
	if err != nil {
		return Address{}, err
	}
	return l.address(postalCode, decoded)
}
