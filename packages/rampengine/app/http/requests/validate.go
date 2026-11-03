// Package requests runs a controller's form request and answers its failure,
// so a handler deals with one outcome:
//
//	var req usersrequests.StoreRequest
//	if response := requests.Validate(ctx, &req); response != nil {
//		return response
//	}
//
// A controller that also imports its feature's form requests imports those as
// <feature>requests. It serves both audiences. It is not a grab-bag: error
// mapping for a domain belongs to that controller package's mapError, and JSON
// shapes belong to app/http/responses.
package requests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gookit/validate"
	contractshttp "github.com/goravel/framework/contracts/http"
	contractsvalidation "github.com/goravel/framework/contracts/validation"
	"github.com/goravel/framework/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
)

// Validate runs request the way the framework's ValidateRequest does — body,
// query and route params, then Rules() — and returns nil when the handler may
// go on. It differs where the framework's answer would mislead the client:
//
//   - A route param wins over a body or query value of the same name. The
//     framework merges it only when the key is absent, so `GET /x/5?id=7`
//     would bind 7.
//   - An Authorize refusal answers 403 and a malformed body 400; the framework
//     returns both as plain errors, indistinguishable from a server fault.
//   - A POST without a body still runs the rules, so a missing field is a 422.
func Validate(ctx contractshttp.Context, request contractshttp.FormRequest) contractshttp.Response {
	if err := request.Authorize(ctx); err != nil {
		return forbidden(ctx)
	}
	if nullBody(ctx) {
		return malformed(ctx)
	}

	rules := request.Rules(ctx)
	validator, err := ctx.Request().Validate(rules, options(ctx, request, rules)...)
	switch {
	case errors.Is(err, validate.ErrEmptyData):
		return validatePath(ctx, request)
	case malformedBody(err):
		return malformed(ctx)
	case err != nil:
		return responses.InternalError(ctx, fmt.Errorf("validating %T: %w", request, err))
	}
	return bind(ctx, validator, request)
}

// ValidatePath validates a request whose inputs live in the URL: each rule key
// is read from the route first, then from the query or body. A body-less POST
// needs it — the framework refuses to parse an empty body — and so does a
// client that sends one with a JSON content type.
func ValidatePath(ctx contractshttp.Context, request contractshttp.FormRequest) contractshttp.Response {
	if err := request.Authorize(ctx); err != nil {
		return forbidden(ctx)
	}
	return validatePath(ctx, request)
}

func validatePath(ctx contractshttp.Context, request contractshttp.FormRequest) contractshttp.Response {
	rules := request.Rules(ctx)
	data := make(map[string]any, len(rules))
	for key := range rules {
		if value := ctx.Request().Route(key); value != "" {
			data[key] = value
			continue
		}
		data[key] = ctx.Request().Input(key)
	}

	validator, err := facades.Validation().Make(ctx, data, rules, options(ctx, request, rules)...)
	if err != nil {
		return responses.InternalError(ctx, fmt.Errorf("validating %T: %w", request, err))
	}
	return bind(ctx, validator, request)
}

func bind(ctx contractshttp.Context, validator contractsvalidation.Validator, request contractshttp.FormRequest) contractshttp.Response {
	if validator.Fails() {
		return responses.ValidationFailed(ctx, validator.Errors())
	}
	if err := validator.Bind(request); err != nil {
		return responses.InternalError(ctx, fmt.Errorf("binding %T: %w", request, err))
	}
	return nil
}

// options carries the request's optional hooks, as ValidateRequest does, with
// the route params pinned before the request's own PrepareForValidation runs.
func options(ctx contractshttp.Context, request contractshttp.FormRequest, rules map[string]string) []contractsvalidation.Option {
	var opts []contractsvalidation.Option
	if r, ok := request.(contractshttp.FormRequestWithFilters); ok {
		opts = append(opts, validation.Filters(r.Filters(ctx)))
	}
	if r, ok := request.(contractshttp.FormRequestWithMessages); ok {
		opts = append(opts, validation.Messages(r.Messages(ctx)))
	}
	if r, ok := request.(contractshttp.FormRequestWithAttributes); ok {
		opts = append(opts, validation.Attributes(r.Attributes(ctx)))
	}

	prepare, _ := request.(contractshttp.FormRequestWithPrepareForValidation)
	return append(opts, validation.PrepareForValidation(func(_ context.Context, data contractsvalidation.Data) error {
		for key := range rules {
			if value := ctx.Request().Route(key); value != "" {
				if err := data.Set(key, value); err != nil {
					return err
				}
			}
		}
		if prepare == nil {
			return nil
		}
		return prepare.PrepareForValidation(ctx, data)
	}))
}

// nullBody reports a JSON body of `null`. gookit decodes it into a nil map, and
// the framework panics writing the route and query params into it.
func nullBody(ctx contractshttp.Context) bool {
	origin := ctx.Request().Origin()
	if origin.Body == nil || !strings.Contains(origin.Header.Get("Content-Type"), "json") {
		return false
	}
	raw, err := io.ReadAll(origin.Body)
	origin.Body = io.NopCloser(bytes.NewReader(raw))
	return err == nil && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func malformed(ctx contractshttp.Context) contractshttp.Response {
	return responses.Error(ctx, contractshttp.StatusBadRequest, "malformed request body")
}

func malformedBody(err error) bool {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntaxErr) || errors.As(err, &typeErr)
}

func forbidden(ctx contractshttp.Context) contractshttp.Response {
	return responses.Error(ctx, contractshttp.StatusForbidden, "forbidden")
}

// RouteID reads a route segment as a positive integer id. It reports false for
// anything else — empty, zero, signed or not a number — and the caller decides
// what that means for its contract (the admin CRUD answers 404).
func RouteID(ctx contractshttp.Context, key string) (uint, bool) {
	return ParseID(ctx.Request().Route(key))
}

// ParseID reads a decimal string as a positive integer id, for a path value a
// form request already validated as a string.
func ParseID(raw string) (uint, bool) {
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return uint(parsed), true
}

// Paging normalises a list window: page defaults to 1, and a page size that is
// absent or above 100 falls back to 20, so no client turns one request into a
// table scan.
func Paging(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return page, perPage
}
