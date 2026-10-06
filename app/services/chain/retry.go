package chain

import (
	"errors"
	"net"

	"github.com/macrowallets/waas/pkg/httpclient"
)

// ErrUnknownOutcome is a call whose result is not known. A broadcast that
// returns it may already have been accepted. Callers reconcile it; they do
// not send it again.
var ErrUnknownOutcome = errors.New("unknown outcome")

type unknownOutcome struct {
	cause error
}

func (e *unknownOutcome) Error() string {
	if e == nil || e.cause == nil {
		return ErrUnknownOutcome.Error()
	}
	return e.cause.Error()
}

func (e *unknownOutcome) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *unknownOutcome) Is(target error) bool {
	return target == ErrUnknownOutcome
}

// UnknownOutcome marks err as an unknown outcome. The text stays err's text,
// so a provider sentinel underneath still matches.
func UnknownOutcome(err error) error {
	if err == nil {
		return ErrUnknownOutcome
	}
	if errors.Is(err, ErrUnknownOutcome) {
		return err
	}
	return &unknownOutcome{cause: err}
}

// KnownFailure reports a failure that is safe to retry: the call timed out
// before it was sent, the provider returned HTTP 5xx, or the provider
// rate-limited the call. An unknown outcome is not a known failure.
func KnownFailure(err error) bool {
	if err == nil || errors.Is(err, ErrUnknownOutcome) {
		return false
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	if timeoutBeforeSend(err) {
		return true
	}
	status := httpStatus(err)
	return status >= 500 && status <= 599
}

// ClassifyBroadcast marks a broadcast whose result is unknown. A known failure
// and a definite rejection are returned unchanged so a retry can still tell
// them apart. An unknown result is reconciled, not sent again.
func ClassifyBroadcast(err error) error {
	if !ambiguous(err) {
		return err
	}
	return UnknownOutcome(err)
}

func ambiguous(err error) bool {
	if err == nil || KnownFailure(err) || errors.Is(err, ErrUnknownOutcome) || httpclient.IsBuild(err) {
		return false
	}
	if httpclient.IsRead(err) || httpclient.IsRoundtrip(err) {
		return true
	}
	return timedOut(err)
}

func timeoutBeforeSend(err error) bool {
	return httpclient.IsBuild(err) && timedOut(err)
}

func timedOut(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func httpStatus(err error) int {
	var failure *Failure
	if errors.As(err, &failure) && failure != nil {
		return failure.Status
	}
	return 0
}
