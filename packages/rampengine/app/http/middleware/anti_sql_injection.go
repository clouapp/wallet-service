package middleware

import (
	"regexp"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
)

// sqlInjectionPatterns are compiled once at package init. They run against
// already-lowercased input from scanRequest, so all patterns assume lowercase.
//
// Defense-in-depth only — the ORM is the actual SQL-injection guard. These
// patterns intentionally stay conservative to limit false positives on
// legitimate user content (names with apostrophes, free-text fields).
var sqlInjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bunion\s+(all\s+)?select\b`),
	regexp.MustCompile(`\bselect\s+.+\s+from\s+`),
	regexp.MustCompile(`\binsert\s+into\s+`),
	regexp.MustCompile(`\bupdate\s+\S+\s+set\s+`),
	regexp.MustCompile(`\bdelete\s+from\s+`),
	regexp.MustCompile(`\bdrop\s+(table|database)\b`),
	regexp.MustCompile(`\btruncate\s+table\b`),
	regexp.MustCompile(`\balter\s+table\b`),
	regexp.MustCompile(`\b` + `exec` + `(ute)?\s*\(`),
	regexp.MustCompile(`\bxp_cmdshell\b`),
	regexp.MustCompile(`\bload_file\s*\(`),
	regexp.MustCompile(`\binto\s+outfile\b`),
	regexp.MustCompile(`\b(sleep|benchmark)\s*\(`),
	regexp.MustCompile(`\bwaitfor\s+delay\b`),
	// Classic tautologies: ' or 1=1, " or "a"="a, ') or (1=1
	regexp.MustCompile(`['"\)]\s*(or|and)\s+\(?\s*['"]?\w+['"]?\s*=\s*['"]?\w+['"]?`),
	// SQL block comments used to chunk payloads.
	regexp.MustCompile(`/\*.*\*/`),
}

// AntiSQLInjection blocks requests carrying SQL-injection signatures in
// headers, query params, or the parsed body.
func AntiSQLInjection(enabled bool) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if !enabled {
			ctx.Request().Next()
			return
		}

		if scanRequest(ctx, matchesSQLInjection) {
			responses.Error(ctx, contractshttp.StatusForbidden, "request blocked by security policy").Abort()
			return
		}

		ctx.Request().Next()
	}
}

func matchesSQLInjection(lowered string) bool {
	for _, p := range sqlInjectionPatterns {
		if p.MatchString(lowered) {
			return true
		}
	}
	return false
}
