package models

import (
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// Company types (KYB spec §7.2): in Brazil the natures a founder recognizes,
// elsewhere Bridge's own vocabulary, which the rest of the world's forms are
// written in. A provider adapter maps the Brazilian ones onto its own words.
const (
	CompanyTypeMEI  = "mei"
	CompanyTypeEI   = "ei"
	CompanyTypeSLU  = "slu"
	CompanyTypeLTDA = "ltda"
	CompanyTypeSA   = "sa"

	CompanyTypeCorporation = "corporation"
	CompanyTypeLLC         = "llc"
	CompanyTypePartnership = "partnership"
	CompanyTypeSoleProp    = "sole_prop"
	CompanyTypeCooperative = "cooperative"
	CompanyTypeTrust       = "trust"

	CompanyTypeOther = "other"
)

var (
	brazilianCompanyTypes = []string{CompanyTypeMEI, CompanyTypeEI, CompanyTypeSLU, CompanyTypeLTDA, CompanyTypeSA, CompanyTypeOther}
	companyTypes          = []string{
		CompanyTypeCorporation, CompanyTypeLLC, CompanyTypePartnership, CompanyTypeSoleProp,
		CompanyTypeCooperative, CompanyTypeTrust, CompanyTypeOther,
	}
)

// CompanyTypesFor is what the KYB form offers a company incorporated in
// country (any ISO spelling), in the order it offers them; nothing for a
// country that is not one. A fresh slice: callers may keep it.
func CompanyTypesFor(country string) []string {
	code, ok := iso3166.Alpha3(country)
	switch {
	case !ok:
		return nil
	case code == "BRA":
		return slices.Clone(brazilianCompanyTypes)
	}
	return slices.Clone(companyTypes)
}
