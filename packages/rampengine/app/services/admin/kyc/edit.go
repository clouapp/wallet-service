package kyc

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
)

// FieldError is an edit a KYC application rule refuses. Field is in the API's
// spelling (residential_address.subdivision), Rule the rule's name as the
// client application's 422 names it, Message its text.
type FieldError struct {
	Field   string
	Rule    string
	Message string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("admin kyc: %s refused by %s", e.Field, e.Rule)
}

// applyEdit writes the edit onto the declaration, holds what it leaves to the
// rules a client application is held to, then canonicalises the tax id and the
// phone as a submission does — so the digests the repository computes are the
// ones the index compares.
func applyEdit(profile *models.KycProfile, d dto.UpdateKycProfileDTO) error {
	was := profile.TaxIdentity()
	for _, field := range []struct {
		to    *string
		value *string
	}{
		{&profile.TaxID, d.TaxID}, {&profile.FirstName, d.FirstName},
		{&profile.LastName, d.LastName}, {&profile.LegalName, d.LegalName}, {&profile.MotherName, d.MotherName},
		{&profile.Email, d.Email}, {&profile.Phone, d.Phone}, {&profile.BirthDate, d.BirthDate},
		{&profile.Nationality, d.Nationality}, {&profile.Occupation, d.Occupation},
		{&profile.SourceOfFunds, d.SourceOfFunds}, {&profile.AccountPurpose, d.AccountPurpose},
	} {
		setString(field.to, field.value)
	}
	applyAddress(&profile.Address, d.Address)
	if d.MonthlyIncomeUSD != nil {
		profile.MonthlyIncomeUSD = d.MonthlyIncomeUSD
	}
	if d.ExpectedMonthlyVolumeUSD != nil {
		profile.ExpectedMonthlyVolumeUSD = d.ExpectedMonthlyVolumeUSD
	}
	if d.IsPEP != nil {
		profile.IsPEP = d.IsPEP
	}
	settleTaxIdentity(profile, was, d)
	if err := validateEdit(profile, was, d); err != nil {
		return err
	}
	canonicalise(profile)
	return nil
}

// settleTaxIdentity writes down the tax identity the edit leaves: the type and
// country it states, else the ones the profile was read as (was) — so moving a
// country never retypes a tax id, and a profile retained before the type keeps
// the reading it had. It writes nothing when the edit states neither and the
// reading still holds; it does when only the address moved under a profile
// read by it, which would otherwise turn its CPF into a foreign tax id.
func settleTaxIdentity(p *models.KycProfile, was models.TaxIdentity, d dto.UpdateKycProfileDTO) {
	if d.TaxIDType == nil && d.TaxIDCountry == nil && (p.TaxID == "" || p.TaxIdentity() == was) {
		return
	}
	typ, country := was.Type, was.Country
	if d.TaxIDType != nil {
		typ = *d.TaxIDType
	}
	if d.TaxIDCountry != nil {
		country = *d.TaxIDCountry
	}
	identity := models.DeriveTaxIdentity(typ, country, country)
	p.TaxIDType, p.TaxIDCountry = identity.Type, identity.Country
}

func applyAddress(address *models.KycProfileAddress, d *dto.KycProfileAddressDTO) {
	if d == nil {
		return
	}
	for _, field := range []struct {
		to    *string
		value *string
	}{
		{&address.StreetLine1, d.StreetLine1}, {&address.StreetLine2, d.StreetLine2}, {&address.Number, d.Number},
		{&address.Neighborhood, d.Neighborhood}, {&address.City, d.City}, {&address.Subdivision, d.Subdivision},
		{&address.PostalCode, d.PostalCode}, {&address.Country, d.Country},
	} {
		setString(field.to, field.value)
	}
	// Stored as an application sends it (rules.CanonicalPostalCode).
	if d.PostalCode != nil {
		address.PostalCode = rules.CanonicalPostalCode(*d.PostalCode)
	}
}

func setString(field *string, value *string) {
	if value != nil {
		*field = strings.TrimSpace(*value)
	}
}

// fieldCheck is one rule over the edited profile: it runs when the edit
// touched the field or what the field is read against (its country).
type fieldCheck struct {
	field, rule string
	touched     bool
	passes      bool
	message     string
}

// requiredMessage is the text a cleared required field is refused with.
const requiredMessage = "is required and cannot be cleared"

// validateEdit holds the edited profile to the application's rules
// (SubmitApplicationRequest): the fields it requires are not cleared, the tax
// identity is coherent (taxIdentityChecks), the tax id is valid for that
// identity — its type and issuing country, never the address — the phone,
// state and postal code for the address's country, the street lines (no PO
// box in the United States), and the identity fits the
// residence (residentIdentity). A field is checked when
// the edit sent it or moved what it is read against, so an edit is not
// refused over a field it did not touch.
func validateEdit(p *models.KycProfile, was models.TaxIdentity, d dto.UpdateKycProfileDTO) error {
	address := d.Address
	if address == nil {
		address = &dto.KycProfileAddressDTO{}
	}
	addressMoved := address.Country != nil
	identityStated := d.TaxIDType != nil || d.TaxIDCountry != nil
	checks := slices.Concat([]fieldCheck{
		required("first_name", d.FirstName), required("last_name", d.LastName),
		required("email", d.Email), required("birth_date", d.BirthDate),
		required("residential_address.street_line_1", address.StreetLine1),
		required("residential_address.city", address.City),
		required("residential_address.country", address.Country),
		required("tax_id_type", d.TaxIDType),
	}, taxIdentityChecks(p, was, d), []fieldCheck{
		{field: "tax_id", rule: "kyc_tax_id", touched: d.TaxID != nil || identityStated,
			passes:  p.TaxID == "" || rules.ValidTaxID(p.TaxID, p.TaxIdentity()),
			message: (&rules.KycTaxIdRule{}).Message(context.Background())},
		{field: "phone", rule: "kyc_phone", touched: d.Phone != nil || addressMoved,
			passes:  rules.ValidKycPhone(p.Phone, p.Address.Country),
			message: (&rules.KycPhoneRule{}).Message(context.Background())},
		addressCode("residential_address.subdivision", "bridge_subdivision", address.Subdivision != nil || addressMoved,
			p.Address.Subdivision, p.Address.Country, rules.SubdivisionRequired, rules.ValidBridgeSubdivision,
			(&rules.BridgeSubdivisionRule{}).Message(context.Background())),
		addressCode("residential_address.postal_code", "bridge_postal_code", address.PostalCode != nil || addressMoved,
			p.Address.PostalCode, p.Address.Country, rules.PostalCodeRequired, rules.ValidBridgePostalCode,
			(&rules.BridgePostalCodeRule{}).Message(context.Background())),
		{field: "residential_address.street_line_1", rule: "kyc_not_po_box",
			touched: address.StreetLine1 != nil || addressMoved,
			passes:  !rules.IsPoBox(p.Address.StreetLine1, p.Address.Country),
			message: (&rules.KycNotPoBoxRule{}).Message(context.Background())},
		{field: "residential_address.street_line_2", rule: "kyc_not_po_box",
			touched: address.StreetLine2 != nil || addressMoved,
			passes:  !rules.IsPoBox(p.Address.StreetLine2, p.Address.Country),
			message: (&rules.KycNotPoBoxRule{}).Message(context.Background())},
		residentIdentity(p, d, addressMoved),
	})
	for _, check := range checks {
		if check.touched && !check.passes {
			return &FieldError{Field: check.field, Rule: check.rule, Message: check.message}
		}
	}
	return nil
}

// The messages the tax identity's coherence is refused with. The admin web
// app maps the US ones by their text: change none of them.
const (
	cpfMovedMessage        = "is required to move a cpf's country off BRA: state the document's type"
	cpfCountryMessage      = "must be BRA for a cpf"
	usTaxIDCountryMessage  = "must be USA for an ssn or itin"
	passportCountryMessage = "is required for a passport: the country that issued it"
	switchDocumentMessage  = "is required to switch the document's type: send the document of the new type"
	residentCPFMessage     = "must be cpf for a residence in BRA: a resident of Brazil declares a CPF"
	residentUSTaxIDMessage = "must be ssn or itin for a residence in USA: a resident of the United States declares an SSN or ITIN"
	usTaxIDAbroadMessage   = "cannot be ssn or itin for a residence outside USA: an SSN or ITIN is declared by a resident of the United States"
)

// taxIdentityChecks hold the identity an edit stating its type or country
// writes down (as settleTaxIdentity wrote it: an empty country is not read
// from the address) to what the application's kyc_tax_id holds it to: a cpf
// is issued by BRA, a passport names the country that issued it, an ssn or an
// itin is USA's. A cpf moved
// off BRA without a type is refused on tax_id_type — the edit has to say what
// the document now is; one stated with another country, on tax_id_country. A
// switch of type brings what the new type needs: a passport the country that
// issued it, since the one a CPF leaves behind is not one a passport was
// issued by; and either one its document, since the old one read as the new
// type would be a CPF's digits taken for a passport number, its reservation
// released. A switch that clears the document, or of a profile with none, has
// nothing to misread.
func taxIdentityChecks(p *models.KycProfile, was models.TaxIdentity, d dto.UpdateKycProfileDTO) []fieldCheck {
	identity := models.TaxIdentity{Type: p.TaxIDType, Country: p.TaxIDCountry}
	touched := d.TaxIDType != nil || d.TaxIDCountry != nil
	switched := d.TaxIDType != nil && identity.Type != was.Type
	cpfAbroad := identity.IsCPF() && identity.Country != "BRA"
	passportUnissued := identity.Type == models.TaxIDTypePassport &&
		(identity.Country == "" || (d.TaxIDCountry == nil && was.Type != models.TaxIDTypePassport))
	return []fieldCheck{
		{field: "tax_id_type", rule: "required", message: cpfMovedMessage,
			touched: touched && d.TaxIDType == nil, passes: !cpfAbroad},
		{field: "tax_id_country", rule: "in", message: cpfCountryMessage,
			touched: touched && d.TaxIDType != nil, passes: !cpfAbroad},
		{field: "tax_id_country", rule: "in", message: usTaxIDCountryMessage,
			touched: touched, passes: !identity.IsUSTaxID() || identity.Country == "USA"},
		{field: "tax_id_country", rule: "required", message: passportCountryMessage,
			touched: touched, passes: !passportUnissued},
		{field: "tax_id", rule: "required", message: switchDocumentMessage,
			touched: switched, passes: d.TaxID != nil || p.TaxID == ""},
	}
}

// residentIdentity holds the identity to the residence the edit leaves, as
// kyc_tax_id holds an application (models.TaxIdentity.FitsResidence): a
// resident of Brazil declares a CPF, of the United States an SSN or an ITIN,
// and nobody else an SSN or an ITIN. So declaring one and moving the other both
// count, while a passport declared as the residence leaves Brazil in the same
// edit is not refused. It runs after the field rules, so a tax id out of shape
// is still refused on tax_id first.
func residentIdentity(p *models.KycProfile, d dto.UpdateKycProfileDTO, addressMoved bool) fieldCheck {
	residence := models.ResidenceCode(p.Address.Country)
	message := usTaxIDAbroadMessage
	switch residence {
	case "BRA":
		message = residentCPFMessage
	case "USA":
		message = residentUSTaxIDMessage
	}
	return fieldCheck{field: "tax_id_type", rule: "in", message: message,
		touched: d.TaxIDType != nil || d.TaxIDCountry != nil || addressMoved,
		passes:  p.TaxIdentity().FitsResidence(residence)}
}

// required refuses clearing a field the application requires.
func required(field string, value *string) fieldCheck {
	return fieldCheck{field: field, rule: "required", message: requiredMessage,
		touched: value != nil, passes: value == nil || strings.TrimSpace(*value) != ""}
}

// addressCode is a state or postal code, required where the application
// requires one for the country (the same predicate SubmitApplicationRequest
// reads; an empty country is Brazil) and valid for it.
func addressCode(field, rule string, touched bool, value, country string,
	requiredIn func(string) bool, valid func(string, string) bool, message string) fieldCheck {
	if value == "" && requiredIn(country) {
		return fieldCheck{field: field, rule: "required", message: requiredMessage, touched: touched}
	}
	return fieldCheck{field: field, rule: rule, message: message, touched: touched,
		passes: value == "" || valid(value, country)}
}

// canonicalise stores the tax id and phone in the form the index digests: a
// CPF by its digits, an SSN or an ITIN by its nine digits, a phone in E.164 for the address's country. validateEdit
// has already refused what does not canonicalise.
func canonicalise(p *models.KycProfile) {
	switch identity := p.TaxIdentity(); {
	case identity.IsCPF():
		p.TaxID = digits.Only(p.TaxID)
	case identity.IsUSTaxID():
		p.TaxID = ustaxid.Canonical(p.TaxID)
	}
	if phone, err := ramp.NormalizePhone(p.Phone, cmp.Or(p.Address.Country, "BRA")); err == nil {
		p.Phone = phone
	}
}
