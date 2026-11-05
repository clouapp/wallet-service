// Package iso3166 answers which strings are ISO 3166-1 countries and converts
// between the alpha-2 and alpha-3 spellings. The wire speaks alpha-3 (BRA);
// Ozav's payload speaks alpha-2 (BR). The data is the CLDR tables compiled
// into golang.org/x/text, so there is no list here to keep up to date.
package iso3166

import (
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

// Country is one ISO 3166-1 entry, both spellings.
type Country struct{ Alpha3, Alpha2 string }

// notISO3166 are the alpha-2 codes x/text reports as countries although
// ISO 3166-1 does not currently assign them: the exceptional reservations
// (AC, CP, DG, TA), France's FQ, Kosovo's user-assigned XK, and the codes
// that were retired (AN, CS, NT, PC, SU, YU) but survive in CLDR's alias
// tables as non-deprecated regions. Found by walking AA..ZZ against
// x/text v0.35: what is left is the 249 officially assigned codes.
var notISO3166 = map[string]struct{}{
	"AC": {}, "AN": {}, "CP": {}, "CS": {}, "DG": {}, "FQ": {}, "NT": {},
	"PC": {}, "SU": {}, "TA": {}, "XK": {}, "YU": {},
}

// countries is built once. x/text has no list of ISO countries, but it says
// whether a code is a country (IsCountry is false for groupings like EU and
// private-use codes like QO). It still accepts deprecated spellings (BU, DD,
// ZR: Canonicalize moves them to another region), codes with no alpha-3
// (UN, EA, IC: ISO3 answers "ZZZ") and the notISO3166 leftovers, so AA..ZZ is
// walked and all of those are filtered out.
var countries = sync.OnceValue(func() []Country {
	var out []Country
	for a := 'A'; a <= 'Z'; a++ {
		for b := 'A'; b <= 'Z'; b++ {
			c, ok := lookup(string([]rune{a, b}))
			if ok {
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alpha3 < out[j].Alpha3 })
	return out
})

// Countries is every ISO 3166-1 country, sorted by Alpha3. Built once from
// golang.org/x/text/language (every two-letter region IsCountry() accepts).
// The slice is shared: callers must not modify it.
func Countries() []Country { return countries() }

// Alpha3 normalises an alpha-2 or alpha-3 code (any case, trimmed) to alpha-3;
// ok=false for anything that is not a country.
func Alpha3(code string) (string, bool) {
	c, ok := lookup(code)
	return c.Alpha3, ok
}

// Alpha2 is the same, to alpha-2.
func Alpha2(code string) (string, bool) {
	c, ok := lookup(code)
	return c.Alpha2, ok
}

// lookup resolves either spelling. ParseRegion is lenient — it also takes
// numeric codes and aliases such as UK or the retired ZR — so the parsed
// region must be canonical and spell the input back exactly, which leaves
// only the real alpha-2 and alpha-3 codes.
func lookup(code string) (Country, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 && len(code) != 3 {
		return Country{}, false
	}
	r, err := language.ParseRegion(code)
	if err != nil || !r.IsCountry() || r.Canonicalize() != r || r.ISO3() == "ZZZ" {
		return Country{}, false
	}
	c := Country{Alpha3: r.ISO3(), Alpha2: r.String()}
	if _, no := notISO3166[c.Alpha2]; no || (code != c.Alpha2 && code != c.Alpha3) {
		return Country{}, false
	}
	return c, true
}
