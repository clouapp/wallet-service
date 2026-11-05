package ramp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync/atomic"
)

// Send performs req on hc and reads the whole answer, for a provider adapter's
// HTTP client.
//
// Every error it returns carries ErrProviderFailed. One returned before a
// connection to the provider existed — a DNS, dial or TLS failure, or a
// context cancelled first — also carries ErrRequestNotSent: the provider
// provably never saw the call, so a create may fail over to another provider.
// Anything after a connection was obtained is unknown upstream state and never
// carries it, however early it looks: it carries ErrOutcomeUnknown instead —
// a timeout, a deadline, a connection reset or closed before the whole answer
// was read.
//
// The line is the connection, not the write. Over HTTP/2 the client returns on
// a cancelled context without waiting for the goroutine that writes the
// request, so a request can be entirely on the wire while its WroteRequest
// trace has not fired yet; GotConn fires on the caller's goroutine, before any
// byte of the request, for both protocols.
//
// A non-2xx answer is not an error: resp and body come back for the adapter to
// classify against its own provider's envelope. resp.Body is already closed.
func Send(hc *http.Client, req *http.Request) (*http.Response, []byte, error) {
	var connected atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}))

	resp, err := hc.Do(req)
	if err != nil {
		// Do's *url.Error prints the URL, query included (Bridge looks a
		// customer up by ?email=); the adapter's wrap already names the call.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		if !connected.Load() {
			return nil, nil, fmt.Errorf("%w: %w: %w", ErrProviderFailed, ErrRequestNotSent, err)
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A body cut short is no answer either: the connection broke under it.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read body: %w", ErrOutcomeUnknown, err)
	}
	return resp, body, nil
}

// Call performs one provider call through do and decodes its answer as T.
//
// do is the adapter's own transport: request type R, a 2xx body decoded into
// out. Call hands it a *json.RawMessage, so the body comes back untouched
// beside the decoded value; that is what the services persist as raw_status.
// A body that does not decode as T is the provider's failure, so it carries
// ErrProviderFailed like every other answer this side cannot use.
//
//	customer, raw, err := ramp.Call[customerResponse](ctx, a.do, req)
func Call[T, R any](ctx context.Context, do func(context.Context, R, any) error, req R) (T, json.RawMessage, error) {
	var decoded T
	var raw json.RawMessage
	if err := do(ctx, req, &raw); err != nil {
		return decoded, nil, err
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return decoded, nil, fmt.Errorf("decode %T: %w: %w", decoded, ErrProviderFailed, err)
	}
	return decoded, raw, nil
}
