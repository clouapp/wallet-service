// Package responses is the single place a JSON body leaves this service.
// Every non-2xx answer is the envelope {"error":{"code","message"}}, with any
// extra fields inside that object. A failed form request stays HTTP 422 with
// an "errors" map. Success bodies stay on Send, which uses
// ctx.Response().Json, so their bytes do not move.
package responses

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"sort"

	contractshttp "github.com/goravel/framework/contracts/http"
	contractsvalidation "github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/resources"
)

const (
	CodeInvalidRequest   = resources.CodeInvalidRequest
	CodeInvalidJSON      = resources.CodeInvalidJSON
	CodeInvalidSignature = resources.CodeInvalidSignature
	CodeUnauthorized     = resources.CodeUnauthorized
	CodeForbidden        = resources.CodeForbidden

	// CodeAccountFrozen is the write refusal for a frozen or archived account.
	// Reads stay allowed. The HTTP status remains 403.
	CodeAccountFrozen = resources.CodeAccountFrozen

	// SuspendedUserMessage is the human text for a platform user suspension.
	// The status is 403 and the code is forbidden: the suspension row of the
	// error contract. account_suspended is the account, not the user.
	SuspendedUserMessage    = "user is suspended"
	CodeNotFound            = resources.CodeNotFound
	CodeConflict            = resources.CodeConflict
	CodeValidationFailed    = resources.CodeValidationFailed
	CodeUnprocessable       = resources.CodeUnprocessable
	CodeTooManyRequests     = resources.CodeTooManyRequests
	CodeRequestTooLarge     = resources.CodeRequestTooLarge
	CodeInternal            = resources.CodeInternal
	CodeProviderUnavailable = resources.CodeProviderUnavailable
	CodeUnavailable         = resources.CodeUnavailable
	CodeTimeout             = resources.CodeTimeout

	// CodeInternalError is the 500 code some routes answer instead of
	// CodeInternal. Both are on the wire today; unifying them is a contract
	// change (http-error-contract.md).
	CodeInternalError = resources.CodeInternalError

	// Domain codes a handler answers with a status other than their default.
	CodeInsufficientFunds             = resources.CodeInsufficientFunds
	CodeWalletNotGasReady             = resources.CodeWalletNotGasReady
	CodeUnsupportedChain              = resources.CodeUnsupportedChain
	CodeSweepLimitExceeded            = resources.CodeSweepLimitExceeded
	CodeSpendingLimitExceeded         = resources.CodeSpendingLimitExceeded
	CodeSpendingLimitInvalid          = resources.CodeSpendingLimitInvalid
	CodeSpendingLimitQuoteUnavailable = resources.CodeSpendingLimitQuoteUnavailable
)

const contentTypeJSON = "application/json"

// internalMessage is the body of InternalError. The cause is logged, never rendered.
const internalMessage = "internal error"

// providerMessage is the body of ProviderError. The upstream text stays in the log.
const providerMessage = "provider unavailable"

// machineCode matches the front parser: a token the client may translate,
// rather than a sentence it should show as written.
var machineCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// messageCodes are human sentences whose stable code is not the status default.
var messageCodes = map[string]string{
	"missing request signature": CodeInvalidSignature,
	"invalid request signature": CodeInvalidSignature,
	"invalid webhook signature": CodeInvalidSignature,
}

// SuspendedUser answers login, refresh and the next session request when
// users.suspended_at is set. The envelope is {"error":{"code","message"}}.
func SuspendedUser(ctx contractshttp.Context) contractshttp.AbortableResponse {
	return Fail(ctx, http.StatusForbidden, CodeForbidden, SuspendedUserMessage)
}

// Send writes body, wrapping a legacy {"error":"text"} map into the envelope.
// A body that is not that legacy shape is written unchanged, through
// ctx.Response().Json so success bytes stay where they are.
func Send(ctx contractshttp.Context, status int, body any) contractshttp.AbortableResponse {
	if wrapped, ok := WrapLegacy(status, body); ok {
		return ctx.Response().Json(status, wrapped)
	}
	return ctx.Response().Json(status, body)
}

// Fail writes the error envelope through ctx.Response().Json, the writer the
// legacy {"error":"text"} maps used: content type application/json;
// charset=utf-8 and no trailing newline. Error writes the same envelope
// through JSON, with a trailing newline; a route keeps the writer it has.
func Fail(ctx contractshttp.Context, status int, code, message string) contractshttp.AbortableResponse {
	return FailWith(ctx, status, code, message, nil)
}

// FailWith is Fail with the contract fields that travel inside the error
// object (limit_type, retry_after_seconds, action, status). A field named
// code or message is ignored.
func FailWith(ctx contractshttp.Context, status int, code, message string, fields map[string]any) contractshttp.AbortableResponse {
	return ctx.Response().Json(status, resources.NewError(resources.ErrorDeps{Code: code, Message: message}).With(fields))
}

// FailMessage is Fail for a message known only at run time: the code is
// CodeFor(status, message), the rule the legacy maps were wrapped with.
func FailMessage(ctx contractshttp.Context, status int, message string) contractshttp.AbortableResponse {
	return Fail(ctx, status, CodeFor(status, message), message)
}

// JSON encodes v with encoding/json and writes it with the given status.
// It is ctx.Response().Data over an explicit json.Encoder, never
// ctx.Response().Json(), so the codec does not depend on a gin build tag.
// An encoding failure answers 500 with the envelope rather than a truncated body.
func JSON(ctx contractshttp.Context, status int, v any) contractshttp.AbortableResponse {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return encodeFailure(ctx)
	}
	return ctx.Response().Data(status, contentTypeJSON, buf.Bytes())
}

// Error writes the strict error envelope. A middleware ends the chain with
// responses.Error(...).Abort().
func Error(ctx contractshttp.Context, status int, code, message string) contractshttp.AbortableResponse {
	return JSON(ctx, status, resources.NewError(resources.ErrorDeps{Code: code, Message: message}))
}

// InternalError logs err and answers 500 without it.
func InternalError(ctx contractshttp.Context, err error) contractshttp.AbortableResponse {
	if err != nil {
		slog.Error("http internal error", "error", err)
	}
	return Error(ctx, http.StatusInternalServerError, resources.CodeInternal, internalMessage)
}

// ProviderError logs err and answers 502. The upstream's own message never
// reaches the body.
func ProviderError(ctx contractshttp.Context, err error) contractshttp.AbortableResponse {
	if err != nil {
		slog.Error("http provider error", "error", err)
	}
	return Error(ctx, http.StatusBadGateway, resources.CodeProviderUnavailable, providerMessage)
}

// ValidationFailed answers a form-request failure with HTTP 422. The per-field
// map is "errors", the key the dashboard parser reads; each value is the
// field's messages in rule-name order.
func ValidationFailed(ctx contractshttp.Context, errs contractsvalidation.Errors) contractshttp.AbortableResponse {
	return FieldsFailed(ctx, FieldMessages(errs))
}

// FieldsFailed answers HTTP 422 with the same envelope as ValidationFailed
// when the messages were built outside a form request.
func FieldsFailed(ctx contractshttp.Context, fields map[string][]string) contractshttp.AbortableResponse {
	return ctx.Response().Json(http.StatusUnprocessableEntity, resources.NewValidation(fields))
}

// FieldError answers one field a handler checked after its form request
// passed, in the same HTTP 422 shape as ValidationFailed. An empty message
// still names the field.
func FieldError(ctx contractshttp.Context, field, message string) contractshttp.AbortableResponse {
	if message == "" {
		message = field + " is invalid"
	}
	return FieldsFailed(ctx, map[string][]string{field: {message}})
}

// WrapLegacy converts a legacy error map into the envelope. The bool is false
// when body is not a map whose "error" value is a string.
func WrapLegacy(status int, body any) (resources.ErrorEnvelope, bool) {
	fields, ok := asMap(body)
	if !ok {
		return resources.ErrorEnvelope{}, false
	}
	message, ok := fields["error"].(string)
	if !ok {
		return resources.ErrorEnvelope{}, false
	}
	explicit, _ := fields["code"].(string)
	extra := map[string]any{}
	for key, value := range fields {
		if key == "error" || key == "code" {
			continue
		}
		extra[key] = value
	}
	code := explicit
	if code == "" {
		code = CodeFor(status, message)
	}
	return resources.NewError(resources.ErrorDeps{
		Code:    code,
		Message: message,
	}).With(extra), true
}

// FieldMessages flattens Goravel's {field: {rule: message}} bag into
// {field: [message, ...]} with rule names sorted, so the wire is stable.
func FieldMessages(errs contractsvalidation.Errors) map[string][]string {
	if errs == nil {
		return map[string][]string{}
	}
	all := errs.All()
	fields := make(map[string][]string, len(all))
	for field, rules := range all {
		names := make([]string, 0, len(rules))
		for name := range rules {
			names = append(names, name)
		}
		sort.Strings(names)
		messages := make([]string, 0, len(names))
		for _, name := range names {
			if rules[name] == "" {
				continue
			}
			messages = append(messages, rules[name])
		}
		if len(messages) > 0 {
			fields[field] = messages
		}
	}
	if fields == nil {
		return map[string][]string{}
	}
	return fields
}

// CodeFor is the code of a message whose code the caller does not name: a
// listed sentence (the signature failures), a message that is itself a
// machine code, or the status default. It is the heuristic the legacy maps
// were wrapped with; FailMessage keeps it for messages known only at run
// time, and every message known when the code is written names its code.
func CodeFor(status int, message string) string {
	if code, ok := messageCodes[message]; ok {
		return code
	}
	if machineCode.MatchString(message) {
		return message
	}
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeRequestTooLarge
	case http.StatusUnprocessableEntity:
		return CodeUnprocessable
	case http.StatusTooManyRequests:
		return CodeTooManyRequests
	case http.StatusBadGateway:
		return CodeProviderUnavailable
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	case http.StatusGatewayTimeout:
		return CodeTimeout
	default:
		return CodeInternal
	}
}

// encodeFailure is the one body written without going through JSON, because
// JSON is what just failed. The envelope is two strings and cannot fail to encode.
func encodeFailure(ctx contractshttp.Context) contractshttp.AbortableResponse {
	body := []byte(`{"error":{"code":"` + resources.CodeInternal + `","message":"` + internalMessage + `"}}` + "\n")
	return ctx.Response().Data(http.StatusInternalServerError, contentTypeJSON, body)
}

func asMap(body any) (map[string]any, bool) {
	switch typed := body.(type) {
	case map[string]any:
		return typed, true
	case contractshttp.Json:
		return map[string]any(typed), true
	default:
		return nil, false
	}
}
