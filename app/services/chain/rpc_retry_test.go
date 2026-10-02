package chain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recordedSleeps struct{ delays []time.Duration }

func (r *recordedSleeps) sleep(ctx context.Context, d time.Duration) error {
	r.delays = append(r.delays, d)
	return ctx.Err()
}

func testRetryClient(url string, sleeps *recordedSleeps, maxAttempts int) *RPCClient {
	c := NewRPCClient(url, "", "")
	c.retry = rateLimitRetry{
		maxAttempts: maxAttempts,
		baseDelay:   100 * time.Millisecond,
		maxDelay:    2 * time.Second,
		jitter:      func(limit time.Duration) time.Duration { return limit / 2 },
		sleep:       sleeps.sleep,
	}
	return c
}

func rateLimitedThenOK(failures int32, failure func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failures {
			failure(w)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":42}`))
	}))
	return srv, &calls
}

func TestRPCCallRetriesRateLimitWithExponentialBackoff(t *testing.T) {
	srv, calls := rateLimitedThenOK(3, func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) })
	defer srv.Close()
	sleeps := &recordedSleeps{}

	var slot uint64
	if err := testRetryClient(srv.URL, sleeps, 6).Call(context.Background(), "getSlot", &slot); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if slot != 42 || calls.Load() != 4 {
		t.Fatalf("slot=%d calls=%d", slot, calls.Load())
	}
	want := []time.Duration{150 * time.Millisecond, 250 * time.Millisecond, 450 * time.Millisecond}
	if len(sleeps.delays) != len(want) {
		t.Fatalf("delays %v", sleeps.delays)
	}
	for i := range want {
		if sleeps.delays[i] != want[i] {
			t.Fatalf("delays %v, want %v", sleeps.delays, want)
		}
	}
}

func TestRPCCallHonoursRetryAfterAndCapsIt(t *testing.T) {
	retryAfter := "1"
	srv, _ := rateLimitedThenOK(2, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(http.StatusForbidden)
		retryAfter = "120"
	})
	defer srv.Close()
	sleeps := &recordedSleeps{}

	if err := testRetryClient(srv.URL, sleeps, 6).Call(context.Background(), "getSlot", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(sleeps.delays) != 2 || sleeps.delays[0] != 1050*time.Millisecond || sleeps.delays[1] != 2*time.Second {
		t.Fatalf("delays %v", sleeps.delays)
	}
}

func TestRPCCallRetriesJSONRPCRateLimitError(t *testing.T) {
	srv, calls := rateLimitedThenOK(1, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":429,"message":"Too many requests"}}`))
	})
	defer srv.Close()

	if err := testRetryClient(srv.URL, &recordedSleeps{}, 6).Call(context.Background(), "getSlot", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestRPCCallGivesUpAfterMaxAttempts(t *testing.T) {
	srv, calls := rateLimitedThenOK(100, func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) })
	defer srv.Close()

	err := testRetryClient(srv.URL, &recordedSleeps{}, 3).Call(context.Background(), "getBlock", nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited (HTTP 429) after 3 attempts") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestRPCCallStopsBackingOffWhenContextEnds(t *testing.T) {
	srv, calls := rateLimitedThenOK(100, func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) })
	defer srv.Close()
	c := testRetryClient(srv.URL, &recordedSleeps{}, 6)
	c.retry.sleep = sleepContext
	c.retry.baseDelay = time.Hour
	c.retry.maxDelay = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.Call(ctx, "getSlot", nil)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestRPCCallDoesNotRetryOtherFailures(t *testing.T) {
	srv, calls := rateLimitedThenOK(100, func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) })
	defer srv.Close()

	if err := testRetryClient(srv.URL, &recordedSleeps{}, 6).Call(context.Background(), "getSlot", nil); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestRPCCallTransportErrorOmitsURL(t *testing.T) {
	c := NewRPCClient("http://127.0.0.1:1/v2/secret-api-key", "", "")
	err := c.Call(context.Background(), "getSlot", nil)
	if err == nil || strings.Contains(err.Error(), "secret-api-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"", 0, false},
		{"3", 3 * time.Second, true},
		{"-1", 0, false},
		{"soon", 0, false},
		{now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second, true},
		{now.Add(-5 * time.Second).Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.value, now)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%q: got %v,%v", tc.value, got, ok)
		}
	}
}
