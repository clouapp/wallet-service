package models

import (
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// Tax identity types a person can declare: a Brazilian CPF, a passport, and
// for a resident of the United States an SSN or an ITIN, the only tax ids
// Bridge takes for that residence (KYC USA spec §2.1, §8.1).
const (
	TaxIDTypeCPF      = "cpf"
	TaxIDTypePassport = "passport"
	TaxIDTypeSSN      = "ssn"
	TaxIDTypeITIN     = "itin"
)

// Tax identity types a company can declare, in Bridge's words as the person
// types are (KYB spec §7.2): a Brazilian CNPJ, a US EIN, a UK company
// registration number, a Mexican RFC, a VAT number, or another registration
// of a country with none of those.
const (
	TaxIDTypeCNPJ  = "cnpj"
	TaxIDTypeEIN   = "ein"
	TaxIDTypeCRN   = "crn"
	TaxIDTypeRFC   = "rfc"
	TaxIDTypeVAT   = "vat"
	TaxIDTypeOther = "other"
)

// PersonTaxIDTypes and BusinessTaxIDTypes are the two vocabularies, in the
// order the forms offer them. A request reads its list rather than spelling
// it, so a type added to one never leaks into the other's validation.
var (
	PersonTaxIDTypes   = []string{TaxIDTypeCPF, TaxIDTypePassport, TaxIDTypeSSN, TaxIDTypeITIN}
	BusinessTaxIDTypes = []string{TaxIDTypeCNPJ, TaxIDTypeEIN, TaxIDTypeCRN, TaxIDTypeRFC, TaxIDTypeVAT, TaxIDTypeOther}
)

// TaxIdentity is the kind of tax id a person or a company declared and the
// country (ISO alpha-3) that issued it: for a company, the country it is
// incorporated in.
type TaxIdentity struct{ Type, Country string }

// IsBusiness reports whether the tax id is one a company declares.
func (t TaxIdentity) IsBusiness() bool { return slices.Contains(BusinessTaxIDTypes, t.Type) }

// FitsIncorporation reports whether a company incorporated in country (any
// ISO spelling) may declare t: Brazil a CNPJ, the United States an EIN, the
// United Kingdom a CRN or a VAT number, Mexico an RFC or a VAT number, and
// any other country a VAT number or another registration. A country that is
// not one fits nothing: unlike a residence, an empty incorporation is no
// installed build's Brazil.
func (t TaxIdentity) FitsIncorporation(country string) bool {
	code, ok := iso3166.Alpha3(country)
	if !ok {
		return false
	}
	switch code {
	case "BRA":
		return t.Type == TaxIDTypeCNPJ
	case "USA":
		return t.Type == TaxIDTypeEIN
	case "GBR":
		return t.Type == TaxIDTypeCRN || t.Type == TaxIDTypeVAT
	case "MEX":
		return t.Type == TaxIDTypeRFC || t.Type == TaxIDTypeVAT
	}
	return t.Type == TaxIDTypeVAT || t.Type == TaxIDTypeOther
}

// BusinessTaxIdentitiesFor is what a company incorporated in country may
// declare, each issued by that country, in BusinessTaxIDTypes order; nothing
// for a country that is not one.
func BusinessTaxIdentitiesFor(country string) []TaxIdentity {
	code, ok := iso3166.Alpha3(country)
	if !ok {
		return nil
	}
	var out []TaxIdentity
	for _, typ := range BusinessTaxIDTypes {
		if identity := (TaxIdentity{Type: typ, Country: code}); identity.FitsIncorporation(code) {
			out = append(out, identity)
		}
	}
	return out
}

// IsCPF reports whether the tax id is a Brazilian CPF.
func (t TaxIdentity) IsCPF() bool { return t.Type == TaxIDTypeCPF }

// IsUSTaxID reports whether the tax id is a US Social Security Number or
// Individual Taxpayer Identification Number.
func (t TaxIdentity) IsUSTaxID() bool {
	return t.Type == TaxIDTypeSSN || t.Type == TaxIDTypeITIN
}

// FitsResidence reports whether a person living in residence (any ISO
// spelling; empty is Brazil, as installed builds mean it) may declare t: a
// resident of Brazil declares a CPF, a resident of the United States an SSN or
// an ITIN (Bridge answers 400 to a CPF or a passport there), and anyone else a
// CPF or a passport, never a US tax id. A company's tax id fits no person.
func (t TaxIdentity) FitsResidence(residence string) bool {
	if t.IsBusiness() {
		return false
	}
	switch ResidenceCode(residence) {
	case "BRA":
		return t.IsCPF()
	case "USA":
		return t.IsUSTaxID()
	}
	return !t.IsUSTaxID()
}

// ResidenceCode reads a residence as the KYC rules do: empty is Brazil, a
// country in any ISO spelling is its alpha-3; anything else is kept trimmed
// and upper-cased for validation to refuse.
func ResidenceCode(residence string) string {
	if strings.TrimSpace(residence) == "" {
		return "BRA"
	}
	return normalizeCountry(residence)
}

// DeriveTaxIdentity fills what an application or a retained profile omits, the
// way builds that predate tax_id_type expect: no type → cpf for BRA or an empty
// residence, passport otherwise; no country → BRA for cpf, USA for an ssn or
// an itin, the residence for a passport. Codes are normalised to alpha-3; an
// input that is not a country is kept trimmed and upper-cased for validation
// to reject.
func DeriveTaxIdentity(taxIDType, taxIDCountry, residence string) TaxIdentity {
	residence = normalizeCountry(residence)
	country := normalizeCountry(taxIDCountry)
	typ := strings.ToLower(strings.TrimSpace(taxIDType))

	if typ == "" {
		switch residence {
		case "", "BRA":
			typ = TaxIDTypeCPF
		default:
			typ = TaxIDTypePassport
		}
	}
	if country == "" {
		switch typ {
		case TaxIDTypeCPF:
			country = "BRA"
		case TaxIDTypeSSN, TaxIDTypeITIN:
			country = "USA"
		default:
			country = residence
		}
	}
	return TaxIdentity{Type: typ, Country: country}
}

func normalizeCountry(code string) string {
	if a3, ok := iso3166.Alpha3(code); ok {
		return a3
	}
	return strings.ToUpper(strings.TrimSpace(code))
}
