package kyc

import (
	"strconv"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"
)

// businessDeclaration is the company a business application declares, as the
// KYB keeps it and every adapter reads it: the draft's tax identity and
// number, which no application changes, and the rest as the application
// states it, canonical as the person form's — the company phone in E.164 for
// its registered country, each address as applicationFrom keeps one — with
// the free texts kept only beside the answer that asks for them, and the
// owner list declared complete at attestedAt.
func businessDeclaration(in dto.BusinessApplicationDTO, draft models.BusinessProfile, attestedAt string) models.BusinessProfile {
	profile := models.BusinessProfile{
		TaxIdentity:           draft.TaxIdentity,
		TaxID:                 draft.TaxID,
		LegalName:             strings.TrimSpace(in.LegalName),
		TradeName:             strings.TrimSpace(in.TradeName),
		CompanyType:           in.CompanyType,
		IncorporationDate:     in.IncorporationDate,
		Email:                 strings.TrimSpace(in.Email),
		Phone:                 canonicalPhone(in.Phone, in.RegisteredAddress.Country),
		Website:               strings.TrimSpace(in.Website),
		Industry:              in.Industry,
		Description:           strings.TrimSpace(in.Description),
		RegisteredAddress:     addressFrom(in.RegisteredAddress),
		AccountPurpose:        in.AccountPurpose,
		SourceOfFunds:         in.SourceOfFunds,
		AnnualRevenueBand:     in.AnnualRevenueBand,
		MonthlyVolumeBand:     in.MonthlyVolumeBand,
		RegulatedActivity:     in.RegulatedActivity != nil && *in.RegulatedActivity,
		ConductsMoneyServices: in.ConductsMoneyServices != nil && *in.ConductsMoneyServices,
		OwnershipAttestedAt:   attestedAt,
	}
	if in.PhysicalAddress != nil {
		physical := addressFrom(*in.PhysicalAddress)
		profile.PhysicalAddress = &physical
	}
	if profile.AccountPurpose == "other" {
		profile.AccountPurposeOther = strings.TrimSpace(in.AccountPurposeOther)
	}
	if profile.ConductsMoneyServices {
		profile.MoneyServicesDescription = strings.TrimSpace(in.MoneyServicesDescription)
	}
	return profile
}

// addressFrom is an address as applicationFrom keeps a person's.
func addressFrom(a dto.AddressDTO) models.KycProfileAddress {
	country := models.ResidenceCode(a.Country)
	return models.KycProfileAddress{
		StreetLine1: strings.TrimSpace(a.StreetLine1), StreetLine2: strings.TrimSpace(a.StreetLine2),
		Number: strings.TrimSpace(a.Number), Neighborhood: strings.TrimSpace(a.Neighborhood),
		City: strings.TrimSpace(a.City), Subdivision: subdivisionFor(country, a.Subdivision),
		PostalCode: rules.CanonicalPostalCode(a.PostalCode), Country: country,
	}
}

// businessPeople is the people a business application names, each as the
// person form keeps one (applicationFrom), their identity beside them — its
// type and issuing country derived as a person's are — and the representative
// linked to their own customer, representativeID.
func businessPeople(in dto.BusinessApplicationDTO, representativeID uint) []models.BusinessPerson {
	people := make([]models.BusinessPerson, 0, len(in.AssociatedPersons))
	for _, p := range in.AssociatedPersons {
		person := models.BusinessPerson{
			LocalRef: p.LocalRef, Roles: p.Roles, OwnershipPercent: strings.TrimSpace(p.OwnershipPercent),
			Title: strings.TrimSpace(p.Title), Person: personProfile(p),
		}
		if p.FromMember {
			id := representativeID
			person.IndividualCustomerID = &id
		}
		people = append(people, person)
	}
	return people
}

// personProfile is one person as applicationFrom reads a person's form, the
// tax id kept on the profile with its type and issuing country, as a retained
// profile keeps it: the adapter places it beside the person's document.
func personProfile(p dto.AssociatedPersonDTO) models.KycProfile {
	form := dto.ApplicationDTO{
		FirstName: p.FirstName, LastName: p.LastName, Email: strings.TrimSpace(p.Email), Phone: p.Phone,
		BirthDate: p.BirthDate, TaxID: p.TaxID, TaxIDType: p.TaxIDType, TaxIDCountry: p.TaxIDCountry,
		Nationality: p.Nationality, StreetLine1: p.Address.StreetLine1, StreetLine2: p.Address.StreetLine2,
		Number: p.Address.Number, Neighborhood: p.Address.Neighborhood, City: p.Address.City,
		Subdivision: p.Address.Subdivision, PostalCode: p.Address.PostalCode,
		Country: models.ResidenceCode(p.Address.Country),
	}
	app := applicationFrom(form)
	profile := app.Profile
	identity := taxIdentityOf(form)
	profile.TaxID = taxIDDocument(p.TaxID, identity).Number
	profile.TaxIDType, profile.TaxIDCountry = identity.Type, identity.Country
	return profile
}

// businessApplication is the application a provider receives for the company
// profile and its people, each with the provider's id for them that an
// earlier application left (externalIDs, by local_ref), none for a new one.
func businessApplication(profile models.BusinessProfile, people []models.BusinessPerson, externalIDs map[string]string) ramp.Application {
	app := ramp.Application{Type: models.CustomerTypeBusiness, Business: &profile}
	for _, person := range people {
		app.AssociatedPersons = append(app.AssociatedPersons, ramp.AssociatedPerson{
			LocalRef: person.LocalRef, ExternalID: externalIDs[person.LocalRef],
			Representative: person.IndividualCustomerID != nil, Roles: person.Roles,
			OwnershipPercent: shareOf(person.OwnershipPercent),
			Title:            person.Title, Profile: person.Person,
		})
	}
	return app
}

// shareOf is an owner's share as the number an adapter sends; nobody else's
// is 0. checkBusinessApplication held every share to a number before.
func shareOf(share string) float64 {
	value, err := strconv.ParseFloat(share, 64)
	if err != nil {
		return 0
	}
	return value
}
