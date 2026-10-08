package requests

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// optionalString accepts any present string and skips an absent one, so a
// filter the handler already accepts does not become a new 422.
const optionalString = "string"

// Open is the form-request base for a route whose permission is middleware.
type Open struct{}

func (Open) Authorize(http.Context) error {
	return nil
}

func optionalStringRules(fields ...string) map[string]string {
	rules := make(map[string]string, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		rules[field] = optionalString
	}
	return rules
}

// Validate runs a form request and writes the branch's 422 on rule failure.
// An empty rule map still binds the body; a missing body is not an error.
func Validate(ctx http.Context, req http.FormRequest) http.Response {
	if ctx == nil || req == nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if len(req.Rules(ctx)) == 0 {
		return bindRulelessRequest(ctx, req)
	}

	validationErrors, err := ctx.Request().ValidateRequest(req)
	if err != nil {
		if validationErrors != nil {
			return responses.ValidationFailed(ctx, validationErrors)
		}
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
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
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	return nil
}

func routeValue(ctx http.Context, name string) string {
	if ctx == nil || ctx.Request() == nil || name == "" {
		return ""
	}
	return ctx.Request().Route(name)
}

func queryValue(ctx http.Context, name, fallback string) string {
	if ctx == nil || ctx.Request() == nil || name == "" {
		return fallback
	}
	return ctx.Request().Query(name, fallback)
}

func inputValue(ctx http.Context, name string) string {
	if ctx == nil || ctx.Request() == nil || name == "" {
		return ""
	}
	return ctx.Request().Input(name)
}

func trimmedRoute(ctx http.Context, name string) string {
	return strings.TrimSpace(routeValue(ctx, name))
}
