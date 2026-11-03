package iso3166

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sync"
)

// Address rules per country, as data. Bridge validates every address against
// its own country list: the postal code is required and regex-checked in 178
// countries, and a subdivision is required in 59 countries that issue no
// postal code. The United States is the one country with both: Bridge refuses
// a US address without a state (400 "must be set for provided country",
// sandbox 14/10/2026). Its list is Bridge's less NY, which Bridge's POST
// /customers refuses ("Unsupported subdivision NY") although its country list
// names it, so New York is never offered nor accepted. The request rules, the country catalog and the apps read the
// same data from here instead of each keeping a copy.
//
// address.json holds only what is used: postal_code (anchored RE2, converted
// from Bridge's Ruby \A…\Z to ^(?:…)$) and subdivisions (ISO 3166-2 code
// without the country prefix, as Bridge spells it). Bridge checks only the
// code; the name is what the app's picker searches, so it is the English one
// from Unicode CLDR (common/subdivisions/en.xml: "Dubai" where ISO and Bridge
// say "Dubayy"), without the superscript CLDR adds to a reused name, and
// Bridge's own where CLDR has none. Two names CLDR and Bridge give a sibling
// too are ISO's: UG-331 Omoro, BI-BM Bujumbura Mairie.
//
// To refresh: GET https://api.sandbox.bridge.xyz/v0/lists/countries with an
// API key (each entry has alpha3, postal_code_format and subdivisions), then
// re-run the sweep that posts an address without a subdivision for every
// country without a postal code; the ones Bridge refuses are the ones that
// keep their subdivisions in the file. Then lay CLDR's English names over
// theirs.

//go:embed address.json
var addressJSON []byte

// Subdivision is one ISO 3166-2 subdivision; Code has no country prefix, as
// Bridge spells it.
type Subdivision struct{ Code, Name string }

type addressRule struct {
	PostalCode   string `json:"postal_code"`
	Subdivisions []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"subdivisions"`
}

var addressData = sync.OnceValue(func() map[string]addressRule {
	var m map[string]addressRule
	if err := json.Unmarshal(addressJSON, &m); err != nil {
		panic("iso3166: address.json: " + err.Error())
	}
	return m
})

var postalRegexps = sync.OnceValue(func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp)
	for code, rule := range addressData() {
		if rule.PostalCode != "" {
			m[code] = regexp.MustCompile(rule.PostalCode)
		}
	}
	return m
})

// PostalCodePattern is the anchored regular expression (^…$, RE2) a postal
// code of country must match; "" when the country issues none.
func PostalCodePattern(alpha3 string) string {
	code, ok := Alpha3(alpha3)
	if !ok {
		return ""
	}
	return addressData()[code].PostalCode
}

// RequiredSubdivisions lists the subdivisions an address in country must
// name, nil when it need not name one (every country with a postal code but the
// United States; BRA keeps its own UF rule).
func RequiredSubdivisions(alpha3 string) []Subdivision {
	code, ok := Alpha3(alpha3)
	if !ok {
		return nil
	}
	rule := addressData()[code]
	if len(rule.Subdivisions) == 0 {
		return nil
	}
	out := make([]Subdivision, len(rule.Subdivisions))
	for i, s := range rule.Subdivisions {
		out[i] = Subdivision{Code: s.Code, Name: s.Name}
	}
	return out
}

// ValidPostalCode reports whether code matches the country's pattern (true
// when the country issues none).
func ValidPostalCode(alpha3, code string) bool {
	a3, ok := Alpha3(alpha3)
	if !ok {
		return true
	}
	re, has := postalRegexps()[a3]
	return !has || re.MatchString(code)
}
