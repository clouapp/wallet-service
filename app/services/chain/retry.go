package chain

import (
	"encoding/json"
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

// ClassifyBroadcast marks a broadcast whose result is unknown: the request may
// have reached the network. That is a timeout or a dropped connection after the
// send, an HTTP 5xx (a gateway may have relayed the transaction before it timed
// out or failed) and a 2xx answer that cannot be read. A rejection (an answer
// about the transaction), a rate limit and a request that never left are returned
// unchanged, so a retry can still tell them apart. An unknown result is
// reconciled by its hash, not sent again.
func ClassifyBroadcast(err error) error {
	if !ambiguous(err) {
		return err
	}
	return UnknownOutcome(err)
}

func ambiguous(err error) bool {
	if err == nil || errors.Is(err, ErrUnknownOutcome) || httpclient.IsBuild(err) || errors.Is(err, ErrRateLimited) {
		return false
	}
	if status := httpStatus(err); status >= 500 && status <= 599 {
		return true
	}
	if timeoutBeforeSend(err) {
		return false
	}
	if httpclient.IsRead(err) || httpclient.IsRoundtrip(err) || unreadableAnswer(err) {
		return true
	}
	return timedOut(err)
}

// unreadableAnswer reports a 2xx answer whose body or result did not decode.
func unreadableAnswer(err error) bool {
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &mismatch)
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
