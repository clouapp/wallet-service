// Package resources is the wire shape for HTTP bodies. Error responses use one
// envelope; a failed validation keeps the per-field map beside it.
package resources

import "encoding/json"

// Stable error codes. code is what a client branches on; message is for humans.
const (
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"

	// CodeAccountFrozen is the write refusal for a frozen or archived account.
	// Reads stay allowed. The HTTP status remains 403.
	CodeAccountFrozen = "account_frozen"

	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeInvalidRequest      = "invalid_request"
	CodeInvalidJSON         = "invalid_json"
	CodeInvalidSignature    = "invalid_signature"
	CodeValidationFailed    = "validation_failed"
	CodeUnprocessable       = "unprocessable"
	CodeRequestTooLarge     = "request_too_large"
	CodeTooManyRequests     = "too_many_requests"
	CodeInternal            = "internal"
	CodeProviderUnavailable = "provider_unavailable"
	CodeUnavailable         = "unavailable"
	CodeTimeout             = "timeout"

	// Public withdrawal failure codes. The withdrawal resource carries one of
	// these in failure_reason, and omits that field when the withdrawal has
	// not failed. CodeInternalError is the persisted failure ("internal_error");
	// CodeInternal ("internal") stays the HTTP 500 envelope.
	CodeInsufficientFunds             = "insufficient_funds"
	CodeWalletNotGasReady             = "wallet_not_gas_ready"
	CodeUnsupportedChain              = "unsupported_chain"
	CodeSweepLimitExceeded            = "sweep_limit_exceeded"
	CodeInvalidPassphrase             = "invalid_passphrase"
	CodePassphraseTooShort            = "passphrase_too_short"
	CodeConcurrentWithdrawal          = "concurrent_withdrawal"
	CodeTooManyAttempts               = "too_many_attempts"
	CodeSpendingLimitExceeded         = "spending_limit_exceeded"
	CodeSpendingLimitInvalid          = "spending_limit_invalid"
	CodeSpendingLimitQuoteUnavailable = "spending_limit_quote_unavailable"
	CodeInternalError                 = "internal_error"

	// ValidationMessage is the human text on every HTTP 422 validation body.
	ValidationMessage = "validation failed"
)

// ErrorEnvelope is every non-2xx body that is not a validation failure:
// {"error":{"code","message"}} plus any extra fields that code's contract
// carries inside the error object.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the code, the human message, and optional contract fields.
type ErrorBody struct {
	Code    string
	Message string
	extra   map[string]any
}

// ValidationFailure is the HTTP 422 body: the envelope plus the per-field map
// the dashboard reads under "errors".
type ValidationFailure struct {
	Error  ErrorBody           `json:"error"`
	Errors map[string][]string `json:"errors"`
}

// NewError builds the envelope for one failure.
func NewError(code, message string) ErrorEnvelope {
	return ErrorEnvelope{Error: ErrorBody{Code: code, Message: message}}
}

// With adds contract fields that travel inside the error object. code and
// message already on the body are left as they are.
func (e ErrorEnvelope) With(extra map[string]any) ErrorEnvelope {
	e.Error.extra = extra
	return e
}

// NewValidation builds the 422 body. A nil map is written as an empty object,
// never null.
func NewValidation(fields map[string][]string) ValidationFailure {
	if fields == nil {
		fields = map[string][]string{}
	}
	return ValidationFailure{
		Error: ErrorBody{
			Code:    CodeValidationFailed,
			Message: ValidationMessage,
		},
		Errors: fields,
	}
}

// MarshalJSON writes code and message, then any extra contract fields.
// Keys land in encoding/json's sorted order.
func (b ErrorBody) MarshalJSON() ([]byte, error) {
	payload := make(map[string]any, 2+len(b.extra))
	payload["code"] = b.Code
	payload["message"] = b.Message
	for key, value := range b.extra {
		if key == "code" || key == "message" {
			continue
		}
		payload[key] = value
	}
	return json.Marshal(payload)
}
