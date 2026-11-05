package middleware

import (
	"regexp"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
)

// xssPatterns are compiled once at package init. They run against
// already-lowercased input from scanRequest, so all patterns assume lowercase.
var xssPatterns = []*regexp.Regexp{
	regexp.MustCompile(`<\s*script\b`),
	regexp.MustCompile(`<\s*/\s*script\s*>`),
	regexp.MustCompile(`<\s*iframe\b`),
	regexp.MustCompile(`<\s*embed\b`),
	regexp.MustCompile(`<\s*object\b`),
	regexp.MustCompile(`<\s*svg\b[^>]*\bon\w+\s*=`),
	regexp.MustCompile(`<\s*img\b[^>]*\bon\w+\s*=`),
	regexp.MustCompile(`javascript\s*:`),
	regexp.MustCompile(`vbscript\s*:`),
	regexp.MustCompile(`data\s*:\s*text\s*/\s*html`),
	regexp.MustCompile(`\bon(load|error|click|mouseover|mouseout|focus|blur|submit|change|keydown|keyup|keypress|input|toggle|animationstart|animationend)\s*=`),
	regexp.MustCompile(`\bexpression\s*\(`),
	regexp.MustCompile(`\beval\s*\(`),
	regexp.MustCompile(`\balert\s*\(`),
	regexp.MustCompile(`document\s*\.\s*(cookie|domain|location|write)`),
	regexp.MustCompile(`window\s*\.\s*location`),
}

// AntiXSS blocks requests carrying common XSS signatures in headers, query
// params, or the parsed body. Output-side escaping in templates/JSON encoders
// is still the primary defense — this middleware is defense-in-depth.
func AntiXSS(enabled bool) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if !enabled {
			ctx.Request().Next()
			return
		}

		if scanRequest(ctx, matchesXSS) {
			responses.Error(ctx, contractshttp.StatusForbidden, "request blocked by security policy").Abort()
			return
		}

		ctx.Request().Next()
	}
}

func matchesXSS(lowered string) bool {
	for _, p := range xssPatterns {
		if p.MatchString(lowered) {
			return true
		}
	}
	return false
}
