package ramps

import (
	"github.com/macrowallets/waas/app/services/ramps"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
	"strconv"
)

func address(a em.KycProfileAddress) map[string]any {
	return map[string]any{"street_line_1": a.StreetLine1, "street_line_2": a.StreetLine2, "number": a.Number, "neighborhood": a.Neighborhood, "city": a.City, "subdivision": a.Subdivision, "postal_code": a.PostalCode, "country": a.Country}
}
func NewBusinessProfile(view *ramps.BusinessDeclaration) map[string]any {
	p := view.Profile
	a := p.RegisteredAddress
	if a.Country == "" {
		a.Country = p.TaxIdentity.Country
	}
	out := map[string]any{"legal_name": p.LegalName, "trade_name": p.TradeName, "company_type": p.CompanyType, "incorporation_date": p.IncorporationDate, "email": p.Email, "phone": p.Phone, "website": p.Website, "industry": p.Industry, "description": p.Description, "registered_address": address(a), "account_purpose": p.AccountPurpose, "account_purpose_other": p.AccountPurposeOther, "source_of_funds": p.SourceOfFunds, "annual_revenue_band": p.AnnualRevenueBand, "expected_monthly_volume_band": p.MonthlyVolumeBand, "regulated_activity": strconv.FormatBool(p.RegulatedActivity), "conducts_money_services": strconv.FormatBool(p.ConductsMoneyServices), "money_services_description": p.MoneyServicesDescription, "ownership_attested": p.OwnershipAttestedAt != "", "tax_id_type": p.TaxIdentity.Type, "incorporation_country": p.TaxIdentity.Country}
	if p.PhysicalAddress != nil {
		out["physical_address"] = address(*p.PhysicalAddress)
	}
	if len(view.People) > 0 {
		people := make([]map[string]any, 0, len(view.People))
		for _, person := range view.People {
			profile := person.Person
			row := address(profile.Address)
			row["local_ref"] = person.LocalRef
			row["roles"] = person.Roles
			row["ownership_percent"] = person.OwnershipPercent
			row["title"] = person.Title
			row["from_member"] = person.IndividualCustomerID != nil
			row["first_name"] = profile.FirstName
			row["last_name"] = profile.LastName
			row["email"] = profile.Email
			row["phone"] = profile.Phone
			row["birth_date"] = profile.BirthDate
			row["nationality"] = profile.Nationality
			identity := profile.TaxIdentity()
			row["tax_id_type"] = identity.Type
			row["tax_id_country"] = identity.Country
			row["tax_id"] = profile.TaxID
			if identity.IsUSTaxID() {
				row["tax_id"] = ""
				row["tax_id_last4"] = ustaxid.Last4(profile.TaxID)
			}
			people = append(people, row)
		}
		out["associated_persons"] = people
	}
	return out
}
