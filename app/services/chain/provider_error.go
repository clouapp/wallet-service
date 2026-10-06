package chain

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Sentinels an adapter returns so a service can errors.Is / errors.As them.
// Error text is stable. The provider's own sentence stays on Failure.Cause
// for the redacting log, never on Error().
var (
	// ErrProviderUnavailable is a timeout or a provider that did not answer.
	// Callers map it to HTTP 502 and may retry.
	ErrProviderUnavailable = errors.New("provider unavailable")
	// ErrNotFound means the provider has no such object.
	ErrNotFound = errors.New("not found")
	// ErrInvalidAddress means the address is not valid for this chain.
	ErrInvalidAddress = errors.New("invalid address")
	// ErrInsufficientFunds means the account cannot cover the amount and fee.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrNonce means the provider rejected the transaction nonce.
	ErrNonce = errors.New("nonce error")
	// ErrFee means the provider rejected the fee or could not price it.
	ErrFee = errors.New("fee error")
	// ErrProvider is any other provider rejection. The raw sentence is not Error().
	ErrProvider = errors.New("provider error")
)

const causeLimit = 512

// Failure is a typed adapter error. Error() is the sentinel text. Cause is
// the provider detail, unwrapped for errors.As, and omitted from Error().
type Failure struct {
	Kind  error
	Cause error
}

func (e *Failure) Error() string {
	if e == nil || e.Kind == nil {
		return ErrProvider.Error()
	}
	return e.Kind.Error()
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Failure) Is(target error) bool {
	return e != nil && e.Kind != nil && target == e.Kind
}

// LogValue keeps the provider sentence on the log record. Error() stays stable.
func (e *Failure) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Error()),
		slog.String("cause", CauseText(e)),
	)
}

// Wrap returns a Failure of kind with cause underneath. A nil cause is the sentinel.
func Wrap(kind, cause error) error {
	if kind == nil {
		return cause
	}
	if cause == nil {
		return kind
	}
	return &Failure{Kind: kind, Cause: cause}
}

// Unavailable marks a timeout or a provider that did not answer.
func Unavailable(cause error) error {
	if cause == nil {
		return ErrProviderUnavailable
	}
	var failure *Failure
	if errors.As(cause, &failure) || errors.Is(cause, ErrRateLimited) {
		return cause
	}
	return Wrap(ErrProviderUnavailable, cause)
}

// InvalidAddress marks an address this chain will not accept.
func InvalidAddress(cause error) error {
	return Wrap(ErrInvalidAddress, cause)
}

// Insufficient marks a balance that cannot cover the transfer and its fee.
func Insufficient(cause error) error {
	return Wrap(ErrInsufficientFunds, cause)
}

// KindOf classifies a provider status and message. Zero status uses the message
// only. An unrecognized message returns nil so the caller can keep its own error.
func KindOf(status int, message string) error {
	switch status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrProviderUnavailable
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	msg := strings.ToLower(message)
	// JSON-RPC "method not found" is an unnamed provider rejection, not a missing object.
	if strings.Contains(msg, "method not found") {
		return nil
	}
	switch {
	case hasAny(msg, "invalid address", "invalid checksum", "bad address", "invalid sender", "invalid bitcoin address"):
		return ErrInvalidAddress
	case hasAny(msg, "insufficient funds", "insufficient balance", "insufficient lamports", "insufficient native"):
		return ErrInsufficientFunds
	case hasAny(msg, "nonce too low", "nonce too high", "invalid nonce"):
		return ErrNonce
	case hasAny(msg, "underpriced", "intrinsic gas too low", "max fee per gas", "min relay fee", "replacement transaction underpriced", "fee too low"):
		return ErrFee
	case hasAny(msg, "not found", "does not exist", "could not find", "no such mempool", "account not found"):
		return ErrNotFound
	case hasAny(msg, "timeout", "timed out", "deadline exceeded", "connection refused", "connection reset", "service unavailable", "bad gateway", "temporarily unavailable", "i/o timeout"):
		return ErrProviderUnavailable
	default:
		return nil
	}
}

// KindOrProvider is KindOf, or ErrProvider when the provider said something
// this list does not name. The raw sentence is still not returned as Error().
func KindOrProvider(status int, message string) error {
	if kind := KindOf(status, message); kind != nil {
		return kind
	}
	return ErrProvider
}

// FromProviderHTTP classifies an upstream HTTP failure. The body is the cause.
func FromProviderHTTP(status int, body string) error {
	body = clip(strings.TrimSpace(body))
	var cause error
	if body == "" {
		cause = fmt.Errorf("upstream HTTP %d", status)
	} else {
		cause = fmt.Errorf("upstream HTTP %d: %s", status, body)
	}
	return Wrap(KindOrProvider(status, body), cause)
}

// ClientText is the stable sentence a response may show. A Failure contributes
// its sentinel. Any other error is returned as-is: it was not a provider body.
func ClientText(err error) string {
	if err == nil {
		return ""
	}
	var failure *Failure
	if errors.As(err, &failure) && failure != nil && failure.Kind != nil {
		return failure.Kind.Error()
	}
	return err.Error()
}

// CauseText is the provider detail for a redacting log. It is empty when err
// is not a Failure.
func CauseText(err error) string {
	var failure *Failure
	if !errors.As(err, &failure) || failure == nil || failure.Cause == nil {
		return ""
	}
	return clip(failure.Cause.Error())
}

func hasAny(message string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}

func clip(text string) string {
	if len(text) <= causeLimit {
		return text
	}
	return text[:causeLimit]
}
