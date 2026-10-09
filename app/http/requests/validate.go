package requests

import (
	"errors"
	"fmt"
	"io"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// Open is the form-request base for a route whose permission is middleware.
type Open struct{}

func (Open) Authorize(http.Context) error {
	return nil
}

// FormRequestWithAfter is a form request with checks that need the bound
// request: a CIDR list, a JSON object whose fields a rule cannot reach. Like
// Laravel's FormRequest::after, but After runs only once every rule passed and
// the body bound, so a request with a failing rule answers that rule's errors
// alone. A non-empty map is answered with the same 422 a rule failure gets.
type FormRequestWithAfter interface {
	After(ctx http.Context) map[string][]string
}

// Validate runs a form request and writes the branch's 422 on rule failure.
// An empty rule map still binds the body; a missing body is not an error.
// A request that implements FormRequestWithAfter is checked last.
func Validate(ctx http.Context, req http.FormRequest) http.Response {
	if ctx == nil || req == nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	}
	if response := validateRules(ctx, req); response != nil {
		return response
	}
	if after, ok := req.(FormRequestWithAfter); ok {
		if fields := after.After(ctx); len(fields) > 0 {
			return responses.FieldsFailed(ctx, fields)
		}
	}
	return nil
}

func validateRules(ctx http.Context, req http.FormRequest) http.Response {
	if len(req.Rules(ctx)) == 0 {
		return bindRulelessRequest(ctx, req)
	}

	validationErrors, err := ctx.Request().ValidateRequest(req)
	if err != nil {
		if validationErrors != nil {
			return responses.ValidationFailed(ctx, validationErrors)
		}
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	}
	if validationErrors != nil {
		return responses.ValidationFailed(ctx, validationErrors)
	}
	return nil
}

// Bind fills dest from the request the same way ctx.Request().Bind does.
// Webhook updates use it so an empty or invalid body stays a 400, which is
// the response the handler already returned before the form request existed.
func Bind(ctx http.Context, dest any) error {
	if ctx == nil || ctx.Request() == nil {
		return fmt.Errorf("missing request")
	}
	if dest == nil {
		return fmt.Errorf("missing form request")
	}
	return ctx.Request().Bind(dest)
}

func bindRulelessRequest(ctx http.Context, req http.FormRequest) http.Response {
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil || request.ContentLength == 0 {
		return nil
	}
	if err := ctx.Request().Bind(req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	}
	return nil
}
