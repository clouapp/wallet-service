package ozav

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// UpstreamError is a structured rejection from Ozav. Callers branch on the
// sentinel it unwraps to, never on Error(). It carries no message: Ozav's
// validation errors restate the submitted value (a legal name, a CPF), and
// Error() is logged verbatim by ramp.LogUpstreamCall.
type UpstreamError struct {
	Method, Path, Code string
	// Type is Ozav's error class (authentication_error, rate_limit_error, …);
	// Code is the per-operation reason. They are different fields and neither
	// stands in for the other.
	Type string
	// Param names the rejected field ("individual.legal_name"), never its
	// value; RequestID is what Ozav's support asks for. Without them a
	// rejection could only be debugged by reproducing the call by hand.
	Param     string
	RequestID string
	sentinel  error
}

// Unwrap exposes the ramp sentinel to errors.Is.
func (e *UpstreamError) Unwrap() error { return e.sentinel }

// Is makes every rejection a provider failure on top of whatever sentinel
// Unwrap names, so a code sentinelFor does not know still answers 502.
func (e *UpstreamError) Is(target error) bool { return target == ramp.ErrProviderFailed }

// ProviderRequestID lets the user timeline read Ozav's request id without
// importing this package.
func (e *UpstreamError) ProviderRequestID() string { return e.RequestID }

func (e *UpstreamError) Error() string {
	msg := fmt.Sprintf("ozav: %s %s: upstream rejected (%s)", e.Method, e.Path, e.Code)
	if e.Param != "" {
		msg += fmt.Sprintf(" param=%s", e.Param)
	}
	if e.RequestID != "" {
		msg += fmt.Sprintf(" request_id=%s", e.RequestID)
	}
	return msg
}

// errorEnvelope is what Ozav answers every non-2xx with. Message and each
// detail's message are left undecoded (see UpstreamError).
type errorEnvelope struct {
	Error struct {
		Type      string `json:"type"`
		Code      string `json:"code"`
		Param     string `json:"param"`
		RequestID string `json:"request_id"`
		// A field-level rejection names its field here rather than in param.
		Details []struct {
			Param string `json:"param"`
		} `json:"details"`
	} `json:"error"`
}

// classify turns a non-2xx response into an error the caller can branch on.
func classify(method, path string, status int, body []byte) error {
	var decoded errorEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		// Not the envelope: the status is all the answer names.
		decoded = errorEnvelope{}
	}
	upstream := &UpstreamError{
		Method:    method,
		Path:      path,
		Code:      cmp.Or(decoded.Error.Code, fmt.Sprintf("http_%d", status)),
		Type:      decoded.Error.Type,
		Param:     decoded.Error.Param,
		RequestID: decoded.Error.RequestID,
	}
	if upstream.Param == "" && len(decoded.Error.Details) > 0 {
		upstream.Param = decoded.Error.Details[0].Param
	}
	upstream.sentinel = sentinelFor(path, status, upstream.Code, upstream.Type)
	return upstream
}

// sentinelFor maps the rejections a caller can act on, and nothing else: an
// unknown code resolves to nil and stays opaque rather than being read as a
// condition it may not be. The code is consulted first, then the class, then
// the status: the same 422 carries QUOTE_EXPIRED (re-quote) and
// PROVISIONING_REQUIRED (wait), and a conflict_error carrying
// IDEMPOTENCY_KEY_REUSED is our own key collision. The table is built from
// codes observed against the real API and those its ErrorEnvelope names; the
// catalog Ozav promises is not served.
func sentinelFor(path string, status int, code, errorType string) error {
	switch code {
	case "ENTITLEMENT_REQUIRED", "INSUFFICIENT_SCOPE",
		"PROVISIONING_NOT_IMPLEMENTED", "VERIFICATION_SESSION_NOT_IMPLEMENTED":
		return ramp.ErrProviderNotImplemented
	case "IDEMPOTENCY_KEY_REUSED":
		return ramp.ErrIdempotencyConflict
	case "PROVISIONING_REQUIRED":
		return ramp.ErrOzavPayoutNotProvisioned
	// Live only: the sandbox simulates the terms attestation.
	case "TERMS_ACCEPTANCE_REQUIRED":
		return ramp.ErrTosRequired
	// The session this side holds is dead. Folding either into
	// TERMS_ACCEPTANCE_REQUIRED would send the client back to that same
	// session, and NOT_FOUND must match here, before the 404-under-/customers
	// fallback tells the user to link an account they have.
	case "TERMS_SESSION_EXPIRED":
		return ramp.ErrTosSessionExpired
	case "TERMS_SESSION_NOT_FOUND":
		return ramp.ErrTosSessionUnknown
	// PROVISIONING_REQUIRED named from the customer's side: it clears on its
	// own.
	case "CUSTOMER_NOT_ENABLED":
		return ramp.ErrRetryableUpstream
	case "QUOTE_EXPIRED":
		return ramp.ErrEstimateExpired
	case "DEPOSIT_IN_FLIGHT", "CHAIN_COVERAGE_INSUFFICIENT", "INSUFFICIENT_BALANCE":
		return ramp.ErrRetryableUpstream
	case "AMOUNT_BELOW_MINIMUM":
		return ramp.ErrAmountBelowMinimum
	// The request named no chain for a destination that does not resolve to
	// one: the ask is incomplete, not the provider down. A new quote naming
	// the chain is what fixes it.
	case "DESTINATION_CHAIN_REQUIRED":
		return ramp.ErrEstimateMismatch
	// PAYOUT_DESTINATION_HOLDER_MISMATCH (Ozav pays Pix only to the customer's
	// own tax id) stays unmapped: no code the apps read means "this key is not
	// yours", and adding one is a contract change.
	// This provider does not serve the combination, and never will by
	// waiting: no_provider_available lets the router move on, where "try
	// again later" would be a lie.
	case "RAIL_NOT_AVAILABLE", "NOT_AVAILABLE",
		"TARGET_CURRENCY_NOT_AVAILABLE", "UNSUPPORTED_COMBINATION":
		return ramp.ErrNoProviderAvailable
	case "API_V2_DISABLED", "AUTH_UNAVAILABLE", "LOOKUP_UNAVAILABLE":
		return ramp.ErrRetryableUpstream
	}

	// The class lives in `type`, never in `code`; matching it on the code
	// silently never fired, and the 401 it missed is the one rejection an admin
	// fixes by rotating a key.
	switch errorType {
	case "authentication_error":
		return ramp.ErrProviderCredentialsInvalid
	case "permission_error":
		return ramp.ErrProviderNotImplemented
	// idempotency_error is not a conflict: Ozav documents it as the
	// idempotency layer being unreachable, with retry as the instruction.
	case "rate_limit_error", "idempotency_error", "api_error":
		return ramp.ErrRetryableUpstream
	}

	switch {
	// A 404 is an integration problem only when the customer is missing; on
	// any other resource it is a bad id.
	case status == http.StatusNotFound && strings.HasPrefix(path, "/customers"):
		return ramp.ErrIntegrationRequired
	case status == http.StatusTooManyRequests:
		return ramp.ErrRetryableUpstream
	// A surface Ozav has not built; its codes are per surface, the status
	// generalizes.
	case status == http.StatusNotImplemented:
		return ramp.ErrProviderNotImplemented
	}
	return nil
}
