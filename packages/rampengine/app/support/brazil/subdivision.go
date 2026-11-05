// Package brazil holds facts about Brazil that more than one layer needs:
// the form rules validate against them and the provider adapters send them.
package brazil

import (
	"maps"
	"slices"
	"strings"
)

// SubdivisionCode maps a state name or code onto its two-letter ISO 3166-2
// code ("Minas Gerais" and "mg" are both "MG"), or reports false when it names
// no Brazilian state.
func SubdivisionCode(subdivision string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(subdivision))
	key = strings.Join(strings.Fields(strings.ReplaceAll(key, "-", " ")), " ")
	key = accentFolder.Replace(key)
	if code, ok := subdivisionNames[key]; ok {
		return code, true
	}
	if code := strings.ToUpper(key); len(code) == 2 && slices.Contains(slices.Collect(maps.Values(subdivisionNames)), code) {
		return code, true
	}
	return "", false
}

var accentFolder = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a",
	"é", "e", "ê", "e",
	"í", "i",
	"ó", "o", "ô", "o", "õ", "o",
	"ú", "u",
	"ç", "c",
)

// subdivisionNames maps the state names, lowercase and unaccented, onto
// their codes; its values are also the set of valid codes.
var subdivisionNames = map[string]string{
	"acre":                "AC",
	"alagoas":             "AL",
	"amapa":               "AP",
	"amazonas":            "AM",
	"bahia":               "BA",
	"ceara":               "CE",
	"distrito federal":    "DF",
	"espirito santo":      "ES",
	"goias":               "GO",
	"maranhao":            "MA",
	"mato grosso":         "MT",
	"mato grosso do sul":  "MS",
	"minas gerais":        "MG",
	"para":                "PA",
	"paraiba":             "PB",
	"parana":              "PR",
	"pernambuco":          "PE",
	"piaui":               "PI",
	"rio de janeiro":      "RJ",
	"rio grande do norte": "RN",
	"rio grande do sul":   "RS",
	"rondonia":            "RO",
	"roraima":             "RR",
	"santa catarina":      "SC",
	"sao paulo":           "SP",
	"sergipe":             "SE",
	"tocantins":           "TO",
}
