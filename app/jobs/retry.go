package jobs

import (
	"time"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	jobRetryLimit = 5
	jobRetryStep  = 5 * time.Second
)

// shouldRetry reports whether the queue may run the job again. A known failure
// — a timeout before the request was sent, an HTTP 5xx, or a rate limit — is
// retried. An unknown outcome, including a broadcast whose result is unknown,
// is not: reconciliation handles it, and the job is not sent again.
func shouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= jobRetryLimit || !chain.KnownFailure(err) {
		return false, 0
	}
	return true, time.Duration(attempt) * jobRetryStep
}
