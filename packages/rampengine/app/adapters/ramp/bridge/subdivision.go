package bridge

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
)

// normalizeSubdivision maps a free-text Brazilian state ("Minas Gerais") onto
// the ISO 3166-2 code Bridge expects ("MG"); Bridge refuses the name as
// invalid_parameters. Anything unresolvable passes through trimmed.
func normalizeSubdivision(country, subdivision string) string {
	subdivision = strings.TrimSpace(subdivision)
	if !strings.EqualFold(country, "BRA") && !strings.EqualFold(country, "BR") {
		return subdivision
	}
	if code, ok := brazil.SubdivisionCode(subdivision); ok {
		return code
	}
	return subdivision
}
