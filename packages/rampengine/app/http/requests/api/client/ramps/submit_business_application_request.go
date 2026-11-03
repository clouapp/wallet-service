package ramps

import (
	"fmt"
	"slices"
	"strconv"

	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// SubmitBusinessApplicationRequest validates POST|PATCH
// /api/kyc/{provider}/applications in a business's scope: the KYB
// application (API contract §5.2) plus the provider and the agreement it
// signed, as SubmitApplicationRequest carries them for a person.
type SubmitBusinessApplicationRequest struct {
	requests.Open
	Provider          string `form:"provider" json:"provider"`
	SignedAgreementID string `form:"signed_agreement_id" json:"signed_agreement_id"`
	BusinessApplicationFields
}

func (r *SubmitBusinessApplicationRequest) Rules(ctx contractshttp.Context) map[string]string {
	rules := businessApplicationRules(ctx)
	rules["provider"] = ProviderRule
	rules["signed_agreement_id"] = "max_len:64"
	return rules
}

// SubmitBusinessApplicationFanOutRequest validates POST /api/kyc/applications
// in a business's scope: the application alone, which the service fans out to
// the providers taking companies (SubmitApplicationFanOutRequest's reasons).
type SubmitBusinessApplicationFanOutRequest struct {
	requests.Open
	BusinessApplicationFields
}

func (r *SubmitBusinessApplicationFanOutRequest) Rules(ctx contractshttp.Context) map[string]string {
	return businessApplicationRules(ctx)
}

// BusinessApplicationFields is a KYB application as the app sends it: the
// company and its activity flat, its addresses nested, its people flat in the
// person form's field names. The country of incorporation and the company's
// document are the draft's, not the body's.
type BusinessApplicationFields struct {
	Type              string                 `form:"type" json:"type"`
	LegalName         string                 `form:"legal_name" json:"legal_name"`
	TradeName         string                 `form:"trade_name" json:"trade_name"`
	CompanyType       string                 `form:"company_type" json:"company_type"`
	IncorporationDate string                 `form:"incorporation_date" json:"incorporation_date"`
	Email             string                 `form:"email" json:"email"`
	Phone             string                 `form:"phone" json:"phone"`
	Website           string                 `form:"website" json:"website"`
	Industry          string                 `form:"industry" json:"industry"`
	Description       string                 `form:"description" json:"description"`
	RegisteredAddress BusinessAddressFields  `form:"registered_address" json:"registered_address"`
	PhysicalAddress   *BusinessAddressFields `form:"physical_address" json:"physical_address"`
	AccountPurpose    string                 `form:"account_purpose" json:"account_purpose"`
	// AccountPurposeOther is read only beside an account_purpose of other.
	AccountPurposeOther       string `form:"account_purpose_other" json:"account_purpose_other"`
	SourceOfFunds             string `form:"source_of_funds" json:"source_of_funds"`
	AnnualRevenueBand         string `form:"annual_revenue_band" json:"annual_revenue_band"`
	ExpectedMonthlyVolumeBand string `form:"expected_monthly_volume_band" json:"expected_monthly_volume_band"`
	// RegulatedActivity and ConductsMoneyServices are pointers: required|bool
	// refuses false, so presence is the service's to check.
	RegulatedActivity        *bool                   `form:"regulated_activity" json:"regulated_activity"`
	ConductsMoneyServices    *bool                   `form:"conducts_money_services" json:"conducts_money_services"`
	MoneyServicesDescription string                  `form:"money_services_description" json:"money_services_description"`
	AssociatedPersons        []AssociatedPersonInput `form:"associated_persons" json:"associated_persons"`
	OwnershipAttested        bool                    `form:"ownership_attested" json:"ownership_attested"`
}

// BusinessAddressFields is a company's address in the person form's address
// fields.
type BusinessAddressFields struct {
	StreetLine1  string `form:"street_line_1" json:"street_line_1"`
	Number       string `form:"number" json:"number"`
	StreetLine2  string `form:"street_line_2" json:"street_line_2"`
	Neighborhood string `form:"neighborhood" json:"neighborhood"`
	City         string `form:"city" json:"city"`
	Subdivision  string `form:"subdivision" json:"subdivision"`
	PostalCode   string `form:"postal_code" json:"postal_code"`
	Country      string `form:"country" json:"country"`
}

// AssociatedPersonInput is one person of the application, in the person
// form's field names so the representative maps 1:1 from GET
// /api/kyc/profile.
type AssociatedPersonInput struct {
	LocalRef   string   `form:"local_ref" json:"local_ref"`
	FromMember bool     `form:"from_member" json:"from_member"`
	Roles      []string `form:"roles" json:"roles"`
	// OwnershipPercent is a JSON number or a numeric string.
	OwnershipPercent any    `form:"ownership_percent" json:"ownership_percent" swaggertype:"number"`
	Title            string `form:"title" json:"title"`
	FirstName        string `form:"first_name" json:"first_name"`
	LastName         string `form:"last_name" json:"last_name"`
	Email            string `form:"email" json:"email"`
	Phone            string `form:"phone" json:"phone"`
	BirthDate        string `form:"birth_date" json:"birth_date"`
	TaxID            string `form:"tax_id" json:"tax_id"`
	TaxIDType        string `form:"tax_id_type" json:"tax_id_type"`
	TaxIDCountry     string `form:"tax_id_country" json:"tax_id_country"`
	Nationality      string `form:"nationality" json:"nationality"`
	StreetLine1      string `form:"street_line_1" json:"street_line_1"`
	Number           string `form:"number" json:"number"`
	StreetLine2      string `form:"street_line_2" json:"street_line_2"`
	Neighborhood     string `form:"neighborhood" json:"neighborhood"`
	City             string `form:"city" json:"city"`
	Subdivision      string `form:"subdivision" json:"subdivision"`
	PostalCode       string `form:"postal_code" json:"postal_code"`
	Country          string `form:"country" json:"country"`
}

// Input is the application as the service takes it.
func (f BusinessApplicationFields) Input() dto.BusinessApplicationDTO {
	in := dto.BusinessApplicationDTO{
		LegalName: f.LegalName, TradeName: f.TradeName, CompanyType: f.CompanyType,
		IncorporationDate: f.IncorporationDate, Email: f.Email, Phone: f.Phone, Website: f.Website,
		Industry: f.Industry, Description: f.Description, RegisteredAddress: f.RegisteredAddress.input(),
		AccountPurpose: f.AccountPurpose, AccountPurposeOther: f.AccountPurposeOther, SourceOfFunds: f.SourceOfFunds,
		AnnualRevenueBand: f.AnnualRevenueBand, MonthlyVolumeBand: f.ExpectedMonthlyVolumeBand,
		RegulatedActivity: f.RegulatedActivity, ConductsMoneyServices: f.ConductsMoneyServices,
		MoneyServicesDescription: f.MoneyServicesDescription, OwnershipAttested: f.OwnershipAttested,
	}
	if f.PhysicalAddress != nil {
		physical := f.PhysicalAddress.input()
		in.PhysicalAddress = &physical
	}
	for _, person := range f.AssociatedPersons {
		in.AssociatedPersons = append(in.AssociatedPersons, person.input())
	}
	return in
}

func (a BusinessAddressFields) input() dto.AddressDTO {
	return dto.AddressDTO{StreetLine1: a.StreetLine1, StreetLine2: a.StreetLine2, Number: a.Number,
		Neighborhood: a.Neighborhood, City: a.City, Subdivision: a.Subdivision, PostalCode: a.PostalCode, Country: a.Country}
}

func (p AssociatedPersonInput) input() dto.AssociatedPersonDTO {
	return dto.AssociatedPersonDTO{
		LocalRef: p.LocalRef, FromMember: p.FromMember, Roles: p.Roles, OwnershipPercent: shareOf(p.OwnershipPercent),
		Title: p.Title, FirstName: p.FirstName, LastName: p.LastName, Email: p.Email, Phone: p.Phone,
		BirthDate: p.BirthDate, TaxID: p.TaxID, TaxIDType: p.TaxIDType, TaxIDCountry: p.TaxIDCountry,
		Nationality: p.Nationality,
		Address: dto.AddressDTO{StreetLine1: p.StreetLine1, StreetLine2: p.StreetLine2, Number: p.Number,
			Neighborhood: p.Neighborhood, City: p.City, Subdivision: p.Subdivision, PostalCode: p.PostalCode,
			Country: p.Country},
	}
}

// shareOf is a share as typed, a JSON number or a numeric string, as the
// decimal string the service reads; anything else is what the service
// refuses as no share.
func shareOf(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// companyTypes is every company type a form may offer, of any country: which
// one the country of incorporation offers is the draft's to say (service).
var companyTypes = slices.Compact(slices.Sorted(slices.Values(
	slices.Concat(models.CompanyTypesFor("BRA"), models.CompanyTypesFor("USA")))))

// businessApplicationRules are the form's rules, written for the people and
// addresses the body carries: each person's under its own index — the key the
// app maps back to the person it sent — and each address's country read beside
// its own fields. What spans fields, the draft or the members is the
// service's (kyc.checkBusinessApplication), and so is a person's required
// field left out, which is profile_incomplete rather than a failed rule.
func businessApplicationRules(ctx contractshttp.Context) map[string]string {
	body := map[string]any{}
	if ctx != nil {
		body = ctx.Request().All()
	}
	table := map[string]string{
		"type":                         "in:" + models.CustomerTypeBusiness,
		"legal_name":                   "required|max_len:256",
		"trade_name":                   "max_len:256",
		"company_type":                 "required|" + kycsvc.RuleIn(companyTypes),
		"incorporation_date":           "required|date_ymd",
		"email":                        "required|email|max_len:128",
		"phone":                        "required|max_len:32|kyc_phone:registered_address",
		"website":                      `max_len:255|regex:^https?://\S+$`,
		"industry":                     "required|" + kycsvc.RuleIn(kycsvc.BusinessIndustryKeys),
		"description":                  "required|min_len:10|max_len:1000",
		"account_purpose":              "required|" + kycsvc.RuleIn(kycsvc.BusinessAccountPurposeKeys),
		"account_purpose_other":        "max_len:255",
		"source_of_funds":              "required|" + kycsvc.RuleIn(kycsvc.BusinessSourceOfFundsKeys),
		"annual_revenue_band":          "required|" + kycsvc.RuleIn(kycsvc.AnnualRevenueBandKeys),
		"expected_monthly_volume_band": "required|" + kycsvc.RuleIn(kycsvc.MonthlyVolumeBandKeys),
		"regulated_activity":           "bool",
		"conducts_money_services":      "bool",
		"money_services_description":   "min_len:10|max_len:1000",
		"associated_persons":           "required",
		"ownership_attested":           "bool",
		"registered_address":           "required",
	}
	addAddressRules(table, "registered_address", nested(body, "registered_address"), true)
	if physical, ok := body["physical_address"].(map[string]any); ok {
		addAddressRules(table, "physical_address", physical, true)
	}
	people, _ := body["associated_persons"].([]any)
	for i := range people {
		addPersonRules(table, "associated_persons."+strconv.Itoa(i), nested(people[i], ""))
	}
	return table
}

// nested is the object under key of value (value itself for an empty key),
// or an empty one.
func nested(value any, key string) map[string]any {
	if key != "" {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	object, _ := value.(map[string]any)
	if object == nil {
		return map[string]any{}
	}
	return object
}

// addAddressRules writes an address's rules under scope: the person form's,
// its country read beside them, the subdivision and postal code required
// where that country uses them when required is true.
func addAddressRules(table map[string]string, scope string, address map[string]any, required bool) {
	country, _ := address["country"].(string)
	presence := ""
	if required {
		presence = "required|"
	}
	subdivision := "max_len:64|bridge_subdivision:" + scope
	if required && rules.SubdivisionRequired(country) {
		subdivision = "required|" + subdivision
	}
	postalCode := "bridge_postal_code:" + scope
	if required && rules.PostalCodeRequired(country) {
		postalCode = "required|" + postalCode
	}
	table[scope+".street_line_1"] = presence + "min_len:4|max_len:128|kyc_not_po_box:" + scope
	table[scope+".street_line_2"] = "kyc_not_po_box:" + scope
	table[scope+".number"] = "max_len:16"
	table[scope+".neighborhood"] = "max_len:64"
	table[scope+".city"] = presence + "max_len:64"
	table[scope+".subdivision"] = subdivision
	table[scope+".postal_code"] = postalCode
	table[scope+".country"] = presence + "len:3|kyc_country"
}

// addPersonRules writes one person's rules under scope. Nothing of a person
// is required here: a field left out is profile_incomplete, which the service
// answers listing every one (API contract §5.3); a field sent is held to the
// person form's rule.
func addPersonRules(table map[string]string, scope string, person map[string]any) {
	table[scope+".local_ref"] = "required|uuid"
	table[scope+".from_member"] = "bool"
	table[scope+".roles"] = "required"
	table[scope+".title"] = "max_len:64"
	table[scope+".first_name"] = "max_len:64"
	table[scope+".last_name"] = "max_len:64"
	table[scope+".email"] = "email|max_len:128"
	table[scope+".phone"] = "max_len:32|kyc_phone:" + scope
	table[scope+".birth_date"] = "kyc_birth_date"
	table[scope+".tax_id"] = "kyc_tax_id:" + scope
	table[scope+".tax_id_type"] = kycsvc.RuleIn(models.PersonTaxIDTypes)
	table[scope+".tax_id_country"] = "len:3|kyc_country"
	table[scope+".nationality"] = "len:3|kyc_country"
	addAddressRules(table, scope, person, false)
}
