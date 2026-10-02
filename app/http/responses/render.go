// Package responses is the only writer of error bodies. Every non-2xx answer
// on /v1 and /api/v1 is the envelope {"error":{"code","message"}}, with any
// extra fields inside that object. Success bodies stay on ctx.Response().Json
// so their bytes do not move.
package responses

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"

	contractshttp "github.com/goravel/framework/contracts/http"
	contractsvalidation "github.com/goravel/framework/contracts/validation"
)

const (
	CodeInvalidRequest      = "invalid_request"
	CodeInvalidJSON         = "invalid_json"
	CodeInvalidSignature    = "invalid_signature"
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeValidationFailed    = "validation_failed"
	CodeUnprocessable       = "unprocessable"
	CodeTooManyRequests     = "too_many_requests"
	CodeRequestTooLarge     = "request_too_large"
	CodeInternal            = "internal"
	CodeProviderUnavailable = "provider_unavailable"
	CodeUnavailable         = "unavailable"
	CodeTimeout             = "timeout"

	validationMessage = "validation failed"
)

// machineCode matches the front parser: a token the client may translate,
// rather than a sentence it should show as written.
var machineCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// messageCodes are human sentences whose stable code is not the status default.
var messageCodes = map[string]string{
	"missing request signature": CodeInvalidSignature,
	"invalid request signature": CodeInvalidSignature,
	"invalid webhook signature": CodeInvalidSignature,
}

// Send writes body, wrapping a legacy {"error":"text"} map into the envelope.
// A body that is not that legacy shape is written unchanged.
func Send(ctx contractshttp.Context, status int, body any) contractshttp.AbortableResponse {
	if wrapped, ok := WrapLegacy(status, body); ok {
		return ctx.Response().Json(status, wrapped)
	}
	return ctx.Response().Json(status, body)
}

// ValidationFailed answers a form-request failure with HTTP 422. The per-field
// map is "errors", the key the dashboard parser reads; each value is the
// field's messages in rule-name order.
func ValidationFailed(ctx contractshttp.Context, errs contractsvalidation.Errors) contractshttp.AbortableResponse {
	return ctx.Response().Json(http.StatusUnprocessableEntity, validationEnvelope{
		Error: errorBody{
			Code:    CodeValidationFailed,
			Message: validationMessage,
		},
		Errors: FieldMessages(errs),
	})
}

// WrapLegacy converts a legacy error map into the envelope. The bool is false
// when body is not a map whose "error" value is a string.
func WrapLegacy(status int, body any) (envelope, bool) {
	fields, ok := asMap(body)
	if !ok {
		return envelope{}, false
	}
	message, ok := fields["error"].(string)
	if !ok {
		return envelope{}, false
	}
	explicit, _ := fields["code"].(string)
	extra := map[string]any{}
	for key, value := range fields {
		if key == "error" || key == "code" {
			continue
		}
		extra[key] = value
	}
	return envelope{Error: errorBody{
		Code:    codeFor(status, message, explicit),
		Message: message,
		extra:   extra,
	}}, true
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

func codeFor(status int, message, explicit string) string {
	if explicit != "" {
		return explicit
	}
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

type envelope struct {
	Error errorBody `json:"error"`
}

type validationEnvelope struct {
	Error  errorBody           `json:"error"`
	Errors map[string][]string `json:"errors"`
}

type errorBody struct {
	Code    string
	Message string
	extra   map[string]any
}

func (b errorBody) MarshalJSON() ([]byte, error) {
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
