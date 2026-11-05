package responses

import (
	"context"
	"errors"
	"strconv"

	contractshttp "github.com/goravel/framework/contracts/http"
	contractsvalidation "github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	otpsvc "github.com/macrowallets/waas/packages/rampengine/app/services/otp"
)

// Error answers status with {"error": message}. A middleware ends the chain
// with responses.Error(...).Abort().
//
// The message is whatever the call site already promised its client: the
// audiences do not share one vocabulary yet ("unauthorized" and
// "unauthenticated" both answer 401), and unifying it is a contract change,
// not something a renderer decides.
func Error(ctx contractshttp.Context, status int, message string) contractshttp.AbortableResponse {
	return ctx.Response().Status(status).Json(ErrorResponse{Error: message})
}

// InternalError logs err and answers 500 without it: the cause can restate
// customer data or internals and never reaches a response body. Callers wrap
// err with what they were doing ("listing users: %w") so the log line says it.
func InternalError(ctx contractshttp.Context, err error) contractshttp.AbortableResponse {
	facades.Log().WithContext(ctx).Error(err)
	// A fired request deadline answers 408, as the framework's timeout did.
	if errors.Is(ctx.Context().Err(), context.DeadlineExceeded) {
		return Error(ctx, contractshttp.StatusRequestTimeout, "request timeout")
	}
	return Error(ctx, contractshttp.StatusInternalServerError, "internal server error")
}

// ProviderError is InternalError for a failure on the provider's side of the
// line: it logs err and answers 502 with message, the endpoint's own words.
// An upstream message can restate customer data, so it never reaches the body.
func ProviderError(ctx contractshttp.Context, err error, message string) contractshttp.Response {
	facades.Log().WithContext(ctx).Error(err)
	return Error(ctx, contractshttp.StatusBadGateway, message)
}

// ValidationFailed answers 422 with every field error the validator produced.
func ValidationFailed(ctx contractshttp.Context, errs contractsvalidation.Errors) contractshttp.Response {
	return ctx.Response().Status(contractshttp.StatusUnprocessableEntity).
		Json(ValidationErrorResponse{Error: "validation failed", Errors: errs.All()})
}

// FieldError answers 422 in the ValidationFailed shape for one field a handler
// checked after its form request passed.
func FieldError(ctx contractshttp.Context, field, rule, message string) contractshttp.Response {
	return ctx.Response().Status(contractshttp.StatusUnprocessableEntity).
		Json(ValidationErrorResponse{Error: "validation failed", Errors: map[string]map[string]string{field: {rule: message}}})
}

// OTPResendTooSoon answers 429 when err is an OTP resend cooldown refusal. The
// body repeats Retry-After for clients that do not read headers.
func OTPResendTooSoon(ctx contractshttp.Context, err error) (contractshttp.Response, bool) {
	left, ok := otpsvc.RetryAfter(err)
	if !ok {
		return nil, false
	}
	secs := otpsvc.Seconds(left)
	return ctx.Response().
		Header("Retry-After", strconv.Itoa(secs)).
		Status(contractshttp.StatusTooManyRequests).
		Json(ErrorResponse{Error: "otp_resend_too_soon", RetryAfterSeconds: secs}), true
}
