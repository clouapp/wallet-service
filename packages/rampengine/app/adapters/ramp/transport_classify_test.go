package ramp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/tests/support/closedport"
)

// Over HTTP/2 the client returns on a cancelled context without waiting for
// the goroutine that writes the request, so the request can be fully on the
// wire while its WroteRequest trace has not fired yet. The server signals the
// moment it holds the whole body and the client cancels right then: every one
// of those requests reached the provider, and one classified not-sent would
// release its reservation and let a retry place the order twice.
func TestSendNeverMarksARequestTheServerReceivedAsNotSent(t *testing.T) {
	got := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		got <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(func() { close(release); srv.Close() })
	hc := srv.Client()

	const calls = 3000
	misclassified := 0
	for i := range calls {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			select {
			case <-got:
				cancel()
			case <-done:
			}
		}()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/transfers", bytes.NewReader(bytes.Repeat([]byte("x"), 200)))
		require.NoError(t, err)
		req.Header.Set("Idempotency-Key", "k-"+strconv.Itoa(i))

		_, _, err = Send(hc, req)
		close(done)
		cancel()
		if errors.Is(err, ErrRequestNotSent) {
			misclassified++
		}
	}
	require.Zero(t, misclassified, "of %d requests the server received, %d were classified not-sent", calls, misclassified)
}

// What each failure means for a create. Not-sent is only a failure before a
// connection existed; everything after it is unknown upstream state, and says
// so (ErrOutcomeUnknown): no answer came back to classify.
func TestSendClassifiesEachFailure(t *testing.T) {
	cases := []struct {
		name    string
		send    func(t *testing.T) error
		notSent bool
	}{
		{name: "dns failure", notSent: true, send: func(t *testing.T) error {
			return post(t, &http.Client{Timeout: time.Second}, "http://unresolvable.invalid/transfers")
		}},
		{name: "connection refused", notSent: true, send: func(t *testing.T) error {
			return post(t, &http.Client{Timeout: time.Second}, "http://"+closedport.Addr(t)+"/transfers")
		}},
		{name: "tls handshake failure", notSent: true, send: func(t *testing.T) error {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)
			return post(t, &http.Client{Timeout: time.Second}, srv.URL+"/transfers") // untrusted certificate
		}},
		{name: "context cancelled before dial", notSent: true, send: func(t *testing.T) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+closedport.Addr(t)+"/transfers", nil)
			require.NoError(t, err)
			_, _, err = Send(&http.Client{Timeout: time.Second}, req)
			return err
		}},
		{name: "server hangs past the timeout", send: func(t *testing.T) error {
			srv := hanging(t, func(w http.ResponseWriter) {})
			return post(t, &http.Client{Timeout: 100 * time.Millisecond}, srv.URL+"/transfers")
		}},
		{name: "server closes after reading the request", send: func(t *testing.T) error {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				conn, _, err := w.(http.Hijacker).Hijack()
				require.NoError(t, err)
				_ = conn.Close()
			}))
			t.Cleanup(srv.Close)
			return post(t, &http.Client{Timeout: time.Second}, srv.URL+"/transfers")
		}},
		{name: "timeout while reading the response body", send: func(t *testing.T) error {
			srv := hanging(t, func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":`))
				w.(http.Flusher).Flush()
			})
			return post(t, &http.Client{Timeout: 100 * time.Millisecond}, srv.URL+"/transfers")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.send(t)
			require.ErrorIs(t, err, ErrProviderFailed)
			if c.notSent {
				require.ErrorIs(t, err, ErrRequestNotSent)
				require.NotErrorIs(t, err, ErrOutcomeUnknown, "nothing reached the provider")
			} else {
				require.NotErrorIs(t, err, ErrRequestNotSent)
				require.ErrorIs(t, err, ErrOutcomeUnknown, "sent, and no answer came back")
			}
		})
	}
}

// A 5xx is an answer, not a transport failure: the adapter classifies it, and
// it is never not-sent.
func TestSendReturnsAServerErrorAsAnAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/transfers", nil)
	require.NoError(t, err)

	resp, _, err := Send(srv.Client(), req)

	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

// The sentinel carries ErrProviderFailed itself, so every caller that only
// knows that one — the HTTP layer's 502 among them — reads it unchanged.
func TestOutcomeUnknownIsAProviderFailure(t *testing.T) {
	require.ErrorIs(t, ErrOutcomeUnknown, ErrProviderFailed)
}

// Go re-sends a POST on its own when a reused keep-alive connection turns out
// to be dead, which is safe only because the re-send carries the same
// Idempotency-Key: the provider sees one order, twice.
func TestSendRetriesADeadKeepAliveWithTheSameIdempotencyKey(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		n := len(keys)
		mu.Unlock()
		if n == 2 { // the second request on the kept-alive connection finds it dead
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	hc := srv.Client()

	require.NoError(t, postKey(t, hc, srv.URL+"/transfers", "first"))
	require.NoError(t, postKey(t, hc, srv.URL+"/transfers", "second"))

	require.Equal(t, []string{"first", "second", "second"}, keys)
}

func post(t *testing.T, hc *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_, _, err = Send(hc, req)
	return err
}

func postKey(t *testing.T, hc *http.Client, url, key string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	req.Header.Set("Idempotency-Key", key)
	_, _, err = Send(hc, req)
	return err
}

// hanging serves one request with start, then holds it open until the test
// ends.
func hanging(t *testing.T, start func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		start(w)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}
