package tron

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	tronRateLimitMaxAttempts = 6
	tronRateLimitBaseDelay   = 250 * time.Millisecond
	tronRateLimitMaxDelay    = 8 * time.Second
	tronJSONRPCRateLimited   = 429
)

// rateLimitRetry spaces retries of rate-limited calls: Retry-After when the provider
// sends it, otherwise exponential backoff, always with jitter and never above maxDelay.
type rateLimitRetry struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	jitter      func(limit time.Duration) time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
}

func defaultRateLimitRetry() rateLimitRetry {
	return rateLimitRetry{
		maxAttempts: tronRateLimitMaxAttempts,
		baseDelay:   tronRateLimitBaseDelay,
		maxDelay:    tronRateLimitMaxDelay,
		jitter:      tronRandomJitter,
		sleep:       tronSleepContext,
	}
}

func (r rateLimitRetry) delay(attempt int, retryAfter string, now time.Time) time.Duration {
	wait, ok := tronParseRetryAfter(retryAfter, now)
	if !ok {
		wait = r.baseDelay << min(attempt-1, 16)
	}
	wait += r.jitter(r.baseDelay)
	return min(wait, r.maxDelay)
}

func tronParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if at, err := httpclient.ParseTime(value); err == nil {
		return max(at.Sub(now), 0), true
	}
	return 0, false
}

func isRateLimited(status int, body []byte) bool {
	if status == httpclient.StatusTooManyRequests || status == httpclient.StatusForbidden {
		return true
	}
	if !bytes.Contains(body, []byte(`"error"`)) {
		return false
	}
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &resp) == nil && resp.Error != nil && resp.Error.Code == tronJSONRPCRateLimited
}

func tronRandomJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}

func tronSleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func withoutURL(err error) error {
	return httpclient.WithoutURL(err)
}
