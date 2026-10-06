package bitcoin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

const jsonRPCRateLimitedCode = 429

// rateLimitRetry spaces retries of rate-limited calls: Retry-After when the provider
// sends it, otherwise exponential backoff, always with jitter and never above maxDelay.
type rateLimitRetry struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	jitter      func(limit time.Duration) time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
}

func (r rateLimitRetry) delay(attempt int, retryAfter string, now time.Time) time.Duration {
	wait, ok := parseRetryAfter(retryAfter, now)
	if !ok {
		wait = r.baseDelay << min(attempt-1, 16)
	}
	wait += r.jitter(r.baseDelay)
	return min(wait, r.maxDelay)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
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

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func isRateLimited(status int, body []byte) bool {
	if status == httpclient.StatusTooManyRequests || status == httpclient.StatusForbidden {
		return true
	}
	if !bytes.Contains(body, []byte(`"error"`)) {
		return false
	}
	var resp rpcResponse
	return json.Unmarshal(body, &resp) == nil && resp.Error != nil && resp.Error.Code == jsonRPCRateLimitedCode
}

func randomJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}

func sleepContext(ctx context.Context, d time.Duration) error {
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

func fmtUnits(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Div(amount, d)
	frac := new(big.Int).Mod(amount, d)
	if frac.Sign() == 0 {
		return whole.String()
	}
	fracStr := strings.TrimRight(fmt.Sprintf("%0*s", decimals, frac.String()), "0")
	return whole.String() + "." + fracStr
}
