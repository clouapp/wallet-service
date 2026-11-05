package blindpay

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// UpstreamError is a structured rejection from BlindPay. Callers branch on the
// sentinel it unwraps to, never on Error().
//
// It deliberately carries neither `description` nor `errors[]`: both can
// restate the value that was submitted (a tax id, an account number), and
// Error() is logged verbatim by ramp.LogUpstreamCall. Code, the legacy message
// and the trace id are enough to find the call on BlindPay's side.
type UpstreamError struct {
	Method, Path string
	Status       int
	// Code is BlindPay's stable code (QUOTES_EXPIRED); Message is the legacy
	// machine string (quote_expired), kept because some conditions are only
	// named there.
	Code, Message string
	TraceID       string
	sentinel      error
}

// Unwrap exposes the ramp sentinel to errors.Is.
func (e *UpstreamError) Unwrap() error { return e.sentinel }

// Is makes every rejection a provider failure on top of whatever sentinel
// Unwrap names, so a code sentinelFor does not know still answers 502.
func (e *UpstreamError) Is(target error) bool { return target == ramp.ErrProviderFailed }

// ProviderRequestID lets the user timeline read BlindPay's trace id without
// importing this package.
func (e *UpstreamError) ProviderRequestID() string { return e.TraceID }

// HTTPStatus lets the bank service tell a refusal of the data (400/422) from
// an outage without importing this package.
func (e *UpstreamError) HTTPStatus() int { return e.Status }

func (e *UpstreamError) Error() string {
	msg := fmt.Sprintf("blindpay: %s %s: upstream rejected (%s)", e.Method, e.Path, e.Code)
	if e.Message != "" {
		msg += " message=" + e.Message
	}
	if e.TraceID != "" {
		msg += " trace_id=" + e.TraceID
	}
	return msg
}

// errorEnvelope is what BlindPay answers every non-2xx with. description and
// errors are left undecoded on purpose (see UpstreamError).
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

// classify turns a non-2xx answer into an UpstreamError. quiet drops the legacy
// message from the error: a request that carries personal data (a Pix key, a
// document) must not let the provider's text, which may echo it, reach a log.
// The sentinel is still read from the message.
func classify(method, path string, status int, body []byte, quiet bool) error {
	var decoded errorEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		decoded = errorEnvelope{}
	}
	upstream := &UpstreamError{
		Method:  method,
		Path:    path,
		Status:  status,
		Code:    cmp.Or(decoded.Code, fmt.Sprintf("http_%d", status)),
		Message: decoded.Message,
		TraceID: decoded.TraceID,
	}
	upstream.sentinel = sentinelFor(status, decoded.Code, decoded.Message)
	if quiet {
		upstream.Message = ""
	}
	return upstream
}

// sentinelFor maps the rejections a caller can act on, and nothing else: an
// unknown code resolves to nil and stays opaque rather than being read as a
// condition it may not be. The stable code is consulted first, then the legacy
// message — which is the only place BlindPay names a terms version change and
// its idempotency outcomes — then the status.
func sentinelFor(status int, code, message string) error {
	switch code {
	case "AUTH_UNAUTHORIZED", "AUTH_TOKEN_INVALID", "INSTANCES_NOT_FOUND":
		return ramp.ErrProviderCredentialsInvalid
	case "AUTH_FORBIDDEN", "AUTH_IP_BLOCKED", "INSTANCES_BLOCKED":
		return ramp.ErrProviderNotImplemented
	case "AUTH_RATE_LIMITED", "AUTH_SERVICE_UNAVAILABLE", "INTERNAL_ERROR", "QUOTES_RATE_UNAVAILABLE":
		return ramp.ErrRetryableUpstream
	case "TOS_NOT_ACCEPTED":
		return ramp.ErrTosRequired
	case "TOS_NOT_FOUND":
		return ramp.ErrTosSessionUnknown
	case "QUOTES_EXPIRED":
		return ramp.ErrEstimateExpired
	case "QUOTES_ALREADY_USED":
		return ramp.ErrEstimateConsumed
	case "CUSTOMERS_NOT_FOUND":
		return ramp.ErrIntegrationRequired
	case "CUSTOMERS_KYC_NOT_APPROVED":
		return ramp.ErrKycPending
	case "PAYOUTS_AMOUNT_BELOW_MINIMUM", "QUOTES_AMOUNT_BELOW_PROVIDER_MINIMUM":
		return ramp.ErrAmountBelowMinimum
	// The pair is not served, and never will be by waiting: no provider
	// available lets the caller move on, where "try again" would be a lie.
	case "BLOCKCHAIN_NETWORK_NOT_SUPPORTED", "BLOCKCHAIN_TOKEN_NOT_SUPPORTED":
		return ramp.ErrNoProviderAvailable
	}

	switch message {
	case "please_accept_terms_of_service":
		return ramp.ErrTosRequired
	case "idempotency_key_payload_mismatch":
		return ramp.ErrIdempotencyConflict
	case "idempotency_key_in_flight":
		return ramp.ErrRetryableUpstream
	}

	switch {
	case status == http.StatusUnauthorized:
		return ramp.ErrProviderCredentialsInvalid
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		return ramp.ErrRetryableUpstream
	}
	return nil
}
