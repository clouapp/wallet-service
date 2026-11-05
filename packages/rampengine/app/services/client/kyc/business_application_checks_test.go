package kyc

import (
	"testing"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/assert"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
)

func boolPtr(b bool) *bool { return &b }

// validCompany is an application every check passes: an LTDA in Brazil, its
// representative an owner with control who signs, and a second owner.
func validCompany() dto.BusinessApplicationDTO {
	address := dto.AddressDTO{StreetLine1: "Rua Augusta", Number: "500", City: "São Paulo", Subdivision: "SP",
		PostalCode: "01304000", Country: "BRA"}
	return dto.BusinessApplicationDTO{
		LegalName: "ACME TECNOLOGIA LTDA", CompanyType: "ltda", IncorporationDate: "2019-03-14",
		Email: "financeiro@acme.com.br", Phone: "+5511987654321", Industry: "software_technology",
		Description: "Desenvolvimento de software", RegisteredAddress: address,
		AccountPurpose: "receive_from_customers", SourceOfFunds: "sales",
		AnnualRevenueBand: "100000_999999", MonthlyVolumeBand: "10000_49999",
		RegulatedActivity: boolPtr(false), ConductsMoneyServices: boolPtr(false),
		AssociatedPersons: []dto.AssociatedPersonDTO{
			{LocalRef: "5b0f2a7e-3c1d-4a8e-9f62-1d2c3b4a5e6f", FromMember: true, Roles: []string{"owner", "control", "signer"},
				OwnershipPercent: "60", Title: "Sócia-administradora", FirstName: "Ana", LastName: "Ribeiro",
				Email: "ana@exemplo.com", Phone: "+5511912345678", BirthDate: "1988-05-20", TaxID: "39053344705",
				TaxIDType: "cpf", TaxIDCountry: "BRA", Address: address},
			{LocalRef: "9c8d7e6f-0000-4000-8000-000000000001", Roles: []string{"owner"}, OwnershipPercent: "40.00",
				FirstName: "Bruno", LastName: "Costa", Email: "bruno@exemplo.com", Phone: "+5511923456789",
				BirthDate: "1990-01-02", TaxID: "52998224725", TaxIDType: "cpf", TaxIDCountry: "BRA", Address: address},
		},
		OwnershipAttested: true,
	}
}

func check(in dto.BusinessApplicationDTO) *FieldRefusal {
	return checkBusinessApplication(in, "BRA", []string{"Ana@Exemplo.com", "bia@exemplo.com"})
}

func TestCheckBusinessApplication_AValidApplicationPasses(t *testing.T) {
	assert.Nil(t, check(validCompany()))
}

// Each rule of the contract (§5.2) answers validation failed on the key and
// rule the app maps to its own copy.
func TestCheckBusinessApplication_RefusesFieldByField(t *testing.T) {
	carbon.SetTestNow(carbon.Parse("2026-10-19 12:00:00"))
	defer carbon.ClearTestNow()
	for _, tc := range []struct {
		name        string
		edit        func(*dto.BusinessApplicationDTO)
		field, rule string
	}{
		{"the company e-mail is a member's", func(a *dto.BusinessApplicationDTO) { a.Email = "BIA@exemplo.com" },
			"email", "distinct_from_members"},
		{"the registered address is abroad", func(a *dto.BusinessApplicationDTO) { a.RegisteredAddress.Country = "PRT" },
			"registered_address.country", "incorporation_country"},
		{"a company type of another country", func(a *dto.BusinessApplicationDTO) { a.CompanyType = "llc" },
			"company_type", "in"},
		{"incorporated tomorrow", func(a *dto.BusinessApplicationDTO) { a.IncorporationDate = "2026-10-20" },
			"incorporation_date", "before_or_equal"},
		{"regulated activity unanswered", func(a *dto.BusinessApplicationDTO) { a.RegulatedActivity = nil },
			"regulated_activity", "required"},
		{"money services unanswered", func(a *dto.BusinessApplicationDTO) { a.ConductsMoneyServices = nil },
			"conducts_money_services", "required"},
		{"money services undescribed", func(a *dto.BusinessApplicationDTO) { a.ConductsMoneyServices = boolPtr(true) },
			"money_services_description", "required"},
		{"another purpose unnamed", func(a *dto.BusinessApplicationDTO) { a.AccountPurpose = "other" },
			"account_purpose_other", "required"},
		{"owners above 100%", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[1].OwnershipPercent = "40.01" },
			"associated_persons", "ownership_total"},
		{"nobody signs", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[0].Roles = []string{"owner", "control"}
		}, "associated_persons", "control_and_signer"},
		{"the representative does not sign", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[0].Roles = []string{"owner", "control"}
			a.AssociatedPersons[1].Roles = []string{"owner", "signer"}
		}, "associated_persons.0.roles", "representative"},
		{"no representative", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[0].FromMember = false },
			"associated_persons", "representative"},
		{"two representatives", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[1].FromMember = true },
			"associated_persons", "representative"},
		{"an MEI with two owners", func(a *dto.BusinessApplicationDTO) { a.CompanyType = "mei" },
			"associated_persons", "sole_proprietorship"},
		{"the owner list not attested", func(a *dto.BusinessApplicationDTO) { a.OwnershipAttested = false },
			"ownership_attested", "accepted"},
		{"a person named twice", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[1].LocalRef = a.AssociatedPersons[0].LocalRef
		}, "associated_persons.1.local_ref", "distinct"},
		{"an unknown role", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[1].Roles = []string{"owner", "investor"}
		}, "associated_persons.1.roles", "in"},
		{"a role twice", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[1].Roles = []string{"owner", "owner"}
		}, "associated_persons.1.roles", "distinct"},
		{"an owner with no share", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[1].OwnershipPercent = "" },
			"associated_persons.1.ownership_percent", "required"},
		{"a share of someone who owns nothing", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[1].Roles = []string{"director"}
		}, "associated_persons.1.ownership_percent", "prohibited"},
		{"a share of three decimals", func(a *dto.BusinessApplicationDTO) {
			a.AssociatedPersons[1].OwnershipPercent = "39.999"
		}, "associated_persons.1.ownership_percent", "ownership_percent"},
		{"a share of nothing", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[1].OwnershipPercent = "0" },
			"associated_persons.1.ownership_percent", "ownership_percent"},
		{"a controller with no title", func(a *dto.BusinessApplicationDTO) { a.AssociatedPersons[0].Title = "" },
			"associated_persons.0.title", "required"},
		{"nine people", func(a *dto.BusinessApplicationDTO) {
			for len(a.AssociatedPersons) < 9 {
				a.AssociatedPersons = append(a.AssociatedPersons, dto.AssociatedPersonDTO{Roles: []string{"director"}})
			}
		}, "associated_persons", "max_len"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validCompany()
			tc.edit(&in)

			refusal := check(in)

			if assert.NotNil(t, refusal) {
				assert.Equal(t, "validation failed", refusal.Code)
				assert.Contains(t, refusal.Fields[tc.field], tc.rule, "%v", refusal.Fields)
			}
		})
	}
}

// A required field of a person left out — the representative pre-filled from
// a profile retained before the tax id was, or typed by someone with no PF
// KYC — is profile_incomplete, every missing field listed (contract §5.3).
func TestCheckBusinessApplication_AMissingPersonFieldIsProfileIncomplete(t *testing.T) {
	in := validCompany()
	in.AssociatedPersons[0].BirthDate = ""
	in.AssociatedPersons[0].TaxID = ""
	in.AssociatedPersons[1].Address.PostalCode = ""

	refusal := check(in)

	if assert.NotNil(t, refusal) {
		assert.Equal(t, "profile_incomplete", refusal.Code)
		assert.Equal(t, map[string]map[string]string{
			"associated_persons.0.birth_date":  {"required": "the birth date is required"},
			"associated_persons.0.tax_id":      {"required": "the tax id is required"},
			"associated_persons.1.postal_code": {"required": "the postal code is required"},
		}, refusal.Fields)
	}
}

// A person living where no postal code or subdivision is used owes neither;
// where one is used, it is owed.
func TestCheckBusinessApplication_AnAddressOwesWhatItsCountryUses(t *testing.T) {
	in := validCompany()
	in.AssociatedPersons[1].Address = dto.AddressDTO{StreetLine1: "Queen's Road 1", City: "Hong Kong", Country: "HKG"}
	assert.Nil(t, check(in))

	in.AssociatedPersons[1].Address = dto.AddressDTO{StreetLine1: "Calle 1", City: "Panamá", Country: "PAN"}
	refusal := check(in)
	if assert.NotNil(t, refusal) {
		assert.Equal(t, map[string]map[string]string{
			"associated_persons.1.subdivision": {"required": "the subdivision is required"},
		}, refusal.Fields)
	}
}
