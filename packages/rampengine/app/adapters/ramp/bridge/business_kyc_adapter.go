package bridge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// submitBusinessApplication is SubmitApplication for an application that
// carries a company (Application.Business): it creates the business customer,
// or updates the one
// the ref names, with the company's data only: its documents and each
// person's photographed ID follow by SubmitDocuments (KYB spec §8, §18.3).
//
// A company e-mail Bridge holds for another customer is never adopted, unlike
// a person's: a company's e-mail proves nothing about whose customer that is.
func (a *Adapter) submitBusinessApplication(ctx context.Context, ref ramp.CustomerRef, app ramp.Application) (*ramp.KycState, error) {
	if ref.ExternalID != "" {
		return a.resubmitBusinessApplication(ctx, ref, app)
	}

	body := businessCustomerBody(app)
	for _, person := range app.AssociatedPersons {
		body.AssociatedPersons = append(body.AssociatedPersons, associatedPersonBodyOf(person, app.Business.OwnershipAttestedAt))
	}
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, Request{
		Method: http.MethodPost, Path: "/customers", Body: body, IdempotencyKey: app.IdempotencyKey,
	})
	if isDuplicateEmailError(err) {
		// The upstream error is not wrapped: its message restates the e-mail.
		return nil, fmt.Errorf("bridge: create business customer: %w", ramp.ErrEmailInUse)
	}
	if err != nil {
		return nil, err
	}
	state := stateFrom(resp, raw)
	state.AssociatedPersonIDs = personIDsByEmail(app.AssociatedPersons, resp.AssociatedPersons)
	// Bridge does not echo the agreement back, so the submitted one is what
	// the service persists.
	state.SignedAgreementID = app.SignedAgreementID
	return state, nil
}

// resubmitBusinessApplication updates the company without restating its
// people, then replaces them: a person Bridge knows is updated, a new one
// created, and one the application no longer lists removed (API contract
// §5.2). The state is read back once every change landed.
func (a *Adapter) resubmitBusinessApplication(ctx context.Context, ref ramp.CustomerRef, app ramp.Application) (*ramp.KycState, error) {
	current, _, err := ramp.Call[customerResponse](ctx, a.do, Request{
		Method: http.MethodPut, Path: customerPath(ref.ExternalID), Body: businessCustomerBody(app),
	})
	if isDuplicateEmailError(err) {
		return nil, fmt.Errorf("bridge: update business customer: %w", ramp.ErrEmailInUse)
	}
	if err != nil {
		return nil, retryableOnStateConflict(err)
	}

	listed := make(map[string]bool, len(current.AssociatedPersons))
	for _, person := range current.AssociatedPersons {
		listed[person.ID] = true
	}
	ids := make(map[string]string, len(app.AssociatedPersons))
	kept := make(map[string]bool, len(app.AssociatedPersons))
	for _, person := range app.AssociatedPersons {
		id, err := a.saveAssociatedPerson(ctx, ref.ExternalID, person, listed[person.ExternalID], app)
		if err != nil {
			return nil, err
		}
		ids[person.LocalRef] = id
		kept[id] = true
	}
	for _, person := range current.AssociatedPersons {
		if kept[person.ID] {
			continue
		}
		if err := a.do(ctx, Request{Method: http.MethodDelete, Path: associatedPersonPath(ref.ExternalID, person.ID)}, nil); err != nil {
			return nil, err
		}
	}

	state, err := a.GetKyc(ctx, ref)
	if err != nil {
		return nil, err
	}
	state.AssociatedPersonIDs = ids
	state.SignedAgreementID = app.SignedAgreementID
	return state, nil
}

// saveAssociatedPerson updates a person Bridge still lists or creates one,
// and answers the person's id.
func (a *Adapter) saveAssociatedPerson(ctx context.Context, customerID string, person ramp.AssociatedPerson, known bool, app ramp.Application) (string, error) {
	body := associatedPersonBodyOf(person, app.Business.OwnershipAttestedAt)
	if known {
		err := a.do(ctx, Request{Method: http.MethodPut, Path: associatedPersonPath(customerID, person.ExternalID), Body: body}, nil)
		return person.ExternalID, retryableOnStateConflict(err)
	}
	var created associatedPersonRef
	req := Request{Method: http.MethodPost, Path: customerPath(customerID) + "/associated_persons", Body: body}
	if app.IdempotencyKey != "" {
		req.IdempotencyKey = app.IdempotencyKey + ":" + person.LocalRef
	}
	if err := a.do(ctx, req, &created); err != nil {
		return "", retryableOnStateConflict(err)
	}
	if created.ID == "" {
		return "", errors.New("bridge: associated person created without an id")
	}
	return created.ID, nil
}

// submitBusinessDocuments places a business's documents: each person's
// identity documents on the person, by the id the application's state kept,
// and then the company's on the customer, by Bridge's purposes. Everything is
// checked before the first call, so a submission never lands half. The people
// go first: a retry after a person Bridge was still processing
// (resource_state_conflict) then finds the company's documents not sent yet,
// rather than uploading them twice. The company's update answers the state
// with everything in; without company documents the state is read back.
func (a *Adapter) submitBusinessDocuments(ctx context.Context, ref ramp.CustomerRef, docs ramp.Documents) (*ramp.KycState, error) {
	companyDocuments, err := companyDocumentPayloads(docs.CompanyDocuments)
	if err != nil {
		return nil, err
	}
	for _, person := range docs.PersonDocuments {
		if person.ExternalID == "" {
			return nil, fmt.Errorf("bridge: associated person %s has no provider id to place its documents on", person.LocalRef)
		}
	}

	for _, person := range docs.PersonDocuments {
		body := personDocumentsBody{IdentifyingInformation: documentsFrom(person.Documents)}
		err := a.do(ctx, Request{Method: http.MethodPut, Path: associatedPersonPath(ref.ExternalID, person.ExternalID), Body: body}, nil)
		if err != nil {
			return nil, documentRefusal(retryableOnStateConflict(err))
		}
	}
	if len(companyDocuments) == 0 {
		return a.GetKyc(ctx, ref)
	}
	state, err := a.putCustomer(ctx, ref.ExternalID, businessCustomerRequest{Documents: companyDocuments})
	return state, documentRefusal(err)
}

// businessCustomerBody is the company in Bridge's words, its people aside.
// A company with no website says none, the physical address is the
// registered one unless the company operates elsewhere, and the activity
// answers Bridge asks only of a high-risk business go always: they cost a
// question and spare an awaiting_questionnaire (KYB spec §7.3).
//
// regulated_activity is left out: Bridge wants a license (its number, the
// regulator and its country) where XIP asks yes or no, and its review asks a
// regulated company for the license (businesses/overview guide).
func businessCustomerBody(app ramp.Application) businessCustomerRequest {
	company := app.Business
	registered := businessAddressFrom(company.RegisteredAddress)
	physical := registered
	if company.PhysicalAddress != nil {
		physical = businessAddressFrom(*company.PhysicalAddress)
	}
	notDAO := false
	conductsMoneyServices := company.ConductsMoneyServices
	body := businessCustomerRequest{
		Type:                      customerTypeBusiness,
		BusinessLegalName:         company.LegalName,
		BusinessTradeName:         company.TradeName,
		BusinessType:              businessTypeFor(company.CompanyType),
		BusinessDescription:       company.Description,
		BusinessIndustry:          businessIndustry(company.Industry),
		Email:                     company.Email,
		Phone:                     company.Phone,
		PrimaryWebsite:            company.Website,
		IncorporationDate:         company.IncorporationDate,
		IsDAO:                     &notDAO,
		RegisteredAddress:         registered,
		PhysicalAddress:           physical,
		IdentifyingInformation:    []identifyingInformation{companyIdentification(company)},
		AccountPurpose:            businessAccountPurposeValues[company.AccountPurpose],
		SourceOfFunds:             businessSourceOfFundsValues[company.SourceOfFunds],
		EstimatedAnnualRevenueUSD: company.AnnualRevenueBand,
		ConductsMoneyServices:     &conductsMoneyServices,
		SignedAgreementID:         app.SignedAgreementID,
		Endorsements:              businessEndorsements(company.TaxIdentity),
	}
	if usd, ok := expectedMonthlyPaymentsUSD(company.MonthlyVolumeBand); ok {
		body.ExpectedMonthlyPaymentsUSD = &usd
	}
	if body.AccountPurpose == "other" {
		body.AccountPurposeOther = company.AccountPurposeOther
	}
	if company.ConductsMoneyServices {
		body.MoneyServicesDescription = company.MoneyServicesDescription
		body.ComplianceScreeningExplanation = company.MoneyServicesDescription
	}
	return body
}

// businessAddressFrom is addressFrom for a business application: the street
// line carries the number after a comma, and the second line the complement
// and the neighbourhood joined by " - ", a part nobody declared left out.
// Bridge checks the company's proof of address against what it receives for
// Pix (BRL ramp migration guide), and a proof of address writes the number
// and the bairro. The people of the company are written the same way; a
// person's own application keeps addressFrom's lines.
func businessAddressFrom(a ramp.Address) *addressPayload {
	payload := addressFrom(a)
	if payload == nil {
		return nil
	}
	payload.StreetLine1 = joinDeclared(", ", a.StreetLine1, a.Number)
	payload.StreetLine2 = joinDeclared(" - ", a.StreetLine2, a.Neighborhood)
	return payload
}

// joinDeclared joins the parts somebody declared, trimmed, with sep.
func joinDeclared(sep string, parts ...string) string {
	declared := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			declared = append(declared, part)
		}
	}
	return strings.Join(declared, sep)
}

// associatedPersonBodyOf is one person: their identity as the identifying
// document Bridge reads a person's tax id from (its image follows with the
// documents), their role flags, an owner's share in whole percent rounded
// down — Bridge takes an integer, and a share is never rounded past what it
// is — a controller's title, and on the representative, the declaration that
// the list of owners is complete.
func associatedPersonBodyOf(person ramp.AssociatedPerson, attestedAt string) associatedPersonBody {
	identity := person.Profile.TaxIdentity()
	body := associatedPersonBody{
		FirstName:          person.Profile.FirstName,
		LastName:           person.Profile.LastName,
		Email:              person.Profile.Email,
		Phone:              person.Profile.Phone,
		BirthDate:          person.Profile.BirthDate,
		ResidentialAddress: businessAddressFrom(person.Profile.Address),
		IdentifyingInformation: documentsFrom([]ramp.IdentifyingDocument{
			{Type: identity.Type, IssuingCountry: identity.Country, Number: person.Profile.TaxID},
		}),
		HasOwnership: person.HasRole(ramp.AssociatedPersonOwner),
		HasControl:   person.HasRole(ramp.AssociatedPersonControl),
		IsSigner:     person.HasRole(ramp.AssociatedPersonSigner),
		IsDirector:   person.HasRole(ramp.AssociatedPersonDirector),
	}
	if body.HasOwnership {
		share := int(math.Floor(person.OwnershipPercent))
		body.OwnershipPercentage = &share
	}
	if body.HasControl {
		body.Title = person.Title
	}
	if person.Representative && body.HasControl {
		body.AttestedOwnershipStructureAt = attestedAt
	}
	return body
}

// companyIdentification is the company's tax id as Bridge reads it: the type,
// the country of incorporation in lower-case alpha-3 as every other issuing
// country goes, and the number — an EIN in its documented NN-NNNNNNN.
func companyIdentification(company *ramp.BusinessProfile) identifyingInformation {
	number := strings.TrimSpace(company.TaxID)
	if company.TaxIdentity.Type == models.TaxIDTypeEIN {
		number = dashedEIN(number)
	}
	return identifyingInformation{
		Type:           company.TaxIdentity.Type,
		IssuingCountry: strings.ToLower(company.TaxIdentity.Country),
		Number:         number,
	}
}

// dashedEIN spells an EIN as Bridge's examples do; one that is not nine
// digits goes as it came.
func dashedEIN(ein string) string {
	digits := strings.ReplaceAll(ein, "-", "")
	if len(digits) != 9 || strings.Trim(digits, "0123456789") != "" {
		return ein
	}
	return digits[:2] + "-" + digits[2:]
}

// businessEndorsements is what a company asks to be endorsed for, decided by
// its own identity and country of incorporation, never by whoever registers
// it: the Pix endorsement of each rail businessRails opens (pix_onramp for the
// deposit, pix_offramp for the withdrawal). The umbrella pix is deprecated
// (01/07/2026). Bridge evaluates every endorsement whatever is asked, so this
// is the intention, not a gate; base joins it with the bank rails.
func businessEndorsements(identity ramp.TaxIdentity) []string {
	var endorsements []string
	for _, rail := range businessRails(identity, identity.Country) {
		switch rail {
		case ramp.RailPixPayin:
			endorsements = append(endorsements, models.EndorsementPixOnramp)
		case ramp.RailPixPayout:
			endorsements = append(endorsements, models.EndorsementPixOfframp)
		}
	}
	return endorsements
}

func businessIndustry(sector string) []string {
	code, ok := businessIndustryValues[sector]
	if !ok {
		return nil
	}
	return []string{code}
}

// companyDocumentPurposes are XIP's company document purposes in Bridge's
// words (businesses/formation guide).
var companyDocumentPurposes = map[string]string{
	ramp.CompanyDocumentFormation:         "business_formation",
	ramp.CompanyDocumentProofOfAddress:    purposeProofOfAddress,
	ramp.CompanyDocumentSourceOfFunds:     "proof_of_source_of_funds",
	ramp.CompanyDocumentTaxIdentification: "proof_of_tax_identification",
	ramp.CompanyDocumentNatureOfBusiness:  "proof_of_nature_of_business",
}

// companyDocumentPayloads names each document by Bridge's purpose. A purpose
// with no word at Bridge is refused rather than dropped: the user would be
// told a document went that never did.
func companyDocumentPayloads(documents []ramp.CompanyDocument) ([]documentPayload, error) {
	var out []documentPayload
	for _, document := range documents {
		purpose, ok := companyDocumentPurposes[document.Purpose]
		if !ok {
			return nil, fmt.Errorf("bridge: no company document purpose %q", document.Purpose)
		}
		out = append(out, documentPayload{Purposes: []string{purpose}, File: document.File})
	}
	return out, nil
}

// personIDsByEmail maps each person the application named onto the id Bridge
// answered for them, by e-mail: the create lists each person by id and e-mail
// only. A person Bridge did not list has no id, and their documents are
// refused until a resubmission gives them one.
func personIDsByEmail(persons []ramp.AssociatedPerson, listed []associatedPersonRef) map[string]string {
	ids := make(map[string]string, len(persons))
	for _, person := range persons {
		for _, ref := range listed {
			if ref.ID != "" && strings.EqualFold(ref.Email, person.Profile.Email) {
				ids[person.LocalRef] = ref.ID
				break
			}
		}
	}
	return ids
}

func associatedPersonPath(customerID, personID string) string {
	return customerPath(customerID) + "/associated_persons/" + personID
}
