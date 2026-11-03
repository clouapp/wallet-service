package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// ErrBusinessExists refuses a business application for a document another
// business past draft took since the draft was opened (contract §5.4): two
// drafts may hold one document, one company may not.
var ErrBusinessExists = errors.New("kyc: a business past draft holds this document")

// errNotABusiness refuses the company's form for a customer that is no
// company: sent, a person's account would reach a provider as a company.
func errNotABusiness(customer *models.Customer) error {
	return fmt.Errorf("kyc: customer %d is a %s, which the company's form does not serve", customer.ID, customer.Type)
}

// SubmitBusinessApplicationToEnabled submits a company's application to every
// enabled provider taking companies that serves it, one job each — the KYB
// twin of SubmitApplicationToEnabled, with the same fan-out rule.
//
// It never passes through checkIdentity, retainProfile or the account
// e-mail check (contract §5.4): the company holds no person's identity, and
// its people's documents reserve nothing in the PF index. What it checks
// before any provider is asked is what the form request cannot see
// (checkBusinessApplication, a *FieldRefusal), then whether another business
// past draft took the draft's document (ErrBusinessExists), then who serves
// the company (ErrJurisdictionNotServed; ramp.ErrNoProviderAvailable when no
// enabled provider takes companies at all). Only then is the declaration —
// the company and its people — kept, right before the first provider that
// receives it is asked, as the person's is retained: the documents call that
// follows reads its people.
//
// user is the member who sends it: the representative, linked to their own
// customer.
func (s *ProviderKycService) SubmitBusinessApplicationToEnabled(
	ctx context.Context, user *models.User, customer *models.Customer, input dto.BusinessApplicationDTO,
) ([]SubmissionOutcome, error) {
	declared, err := s.declareBusiness(user, customer, input)
	if err != nil {
		return nil, err
	}
	providers, err := s.businessProviders(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	// A provider with a submission still on its way answers
	// submission_in_flight. The declaration is kept right before the first
	// provider that will receive it is asked, and only then: kept for a
	// submission that reaches nobody — every provider in flight, or passed
	// over — it would replace the company and the people the submission in
	// flight declared, which nothing then sends.
	inFlight := s.inFlightAt(customer.ID, providers)
	kept := false
	return s.submitToEnabled(ctx, user.ID, customer.ID, providers, rampsvc.OpSubmitApplication, s.applicationSkipCheck(customer.ID),
		func(adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, error) {
			if inFlight[adapter.Code()] {
				return nil, ErrSubmissionInFlight
			}
			if !kept {
				if err := s.keepBusiness(customer.ID, declared); err != nil {
					return nil, err
				}
				kept = true
			}
			return s.submitApplication(ctx, user, customer.ID, adapter.Code(), adapter, declared.app, ramp.Profile{})
		})
}

// inFlightAt is the providers holding a submission of customerID still on its
// way (the staging's in-flight mark).
func (s *ProviderKycService) inFlightAt(customerID uint, providers []ramp.ProviderAdapter) map[string]bool {
	out := map[string]bool{}
	for _, adapter := range providers {
		if s.staging.InFlightKind(customerID, adapter.Code()) != "" {
			out[adapter.Code()] = true
		}
	}
	return out
}

// SubmitBusinessApplication is SubmitBusinessApplicationToEnabled for the one
// provider the path names: a provider that takes no company is
// ramp.ErrNoProviderAvailable (contract §5.1), one that does not serve the
// company ErrJurisdictionNotServed.
func (s *ProviderKycService) SubmitBusinessApplication(
	ctx context.Context, user *models.User, customer *models.Customer, provider string, input dto.BusinessApplicationDTO,
) (*models.CustomerProviderKyc, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	if customer.Type != models.CustomerTypeBusiness {
		return nil, errNotABusiness(customer)
	}
	if _, takes := ramp.BusinessKycOf(adapter); !takes {
		return nil, ramp.ErrNoProviderAvailable
	}
	declared, err := s.declareBusiness(user, customer, input)
	if err != nil {
		return nil, err
	}
	if err := s.refuseUnservedRetained(ctx, customer.ID, provider); err != nil {
		return nil, err
	}
	// Kept only when nothing is on its way there: see the fan-out.
	if s.staging.InFlightKind(customer.ID, provider) != "" {
		return nil, ErrSubmissionInFlight
	}
	if err := s.keepBusiness(customer.ID, declared); err != nil {
		return nil, err
	}
	declared.app.SignedAgreementID = input.SignedAgreementID
	return s.submitApplication(ctx, user, customer.ID, provider, adapter, declared.app, ramp.Profile{})
}

// SubmitBusinessDocumentsToEnabled submits a company's documents to every
// enabled provider taking companies that serves it, one job each, with the
// documents fan-out's filter (documentSkipCheck, on the company's steps).
// Each person's go to a person the last application named, by local_ref;
// the provider's id for them is placed by the worker, which sends after the
// application that names them landed.
func (s *ProviderKycService) SubmitBusinessDocumentsToEnabled(
	ctx context.Context, user *models.User, customer *models.Customer, input dto.BusinessDocumentsDTO,
) ([]SubmissionOutcome, error) {
	docs, err := s.businessDocumentsOf(customer, input)
	if err != nil {
		return nil, err
	}
	providers, err := s.businessProviders(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	return s.submitToEnabled(ctx, user.ID, customer.ID, providers, rampsvc.OpSubmitDocuments, s.documentSkipCheck(customer.ID),
		func(adapter ramp.ProviderAdapter) (*models.CustomerProviderKyc, error) {
			return s.submitDocuments(ctx, user, customer.ID, adapter.Code(), docs)
		})
}

// SubmitBusinessDocuments is SubmitBusinessDocumentsToEnabled for the one
// provider the path names, refused as SubmitBusinessApplication refuses one.
func (s *ProviderKycService) SubmitBusinessDocuments(
	ctx context.Context, user *models.User, customer *models.Customer, provider string, input dto.BusinessDocumentsDTO,
) (*models.CustomerProviderKyc, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	if _, takes := ramp.BusinessKycOf(adapter); !takes {
		return nil, ramp.ErrNoProviderAvailable
	}
	docs, err := s.businessDocumentsOf(customer, input)
	if err != nil {
		return nil, err
	}
	if err := s.refuseUnservedRetained(ctx, customer.ID, provider); err != nil {
		return nil, err
	}
	return s.submitDocuments(ctx, user, customer.ID, provider, docs)
}

// businessDeclaration is a business application once checked: the company
// and its people as kept, and the application every provider receives.
type businessDeclared struct {
	profile models.BusinessProfile
	people  []models.BusinessPerson
	app     ramp.Application
}

// declareBusiness checks input against the company's draft and members and
// answers what it declares, or the refusal: a customer that is no business, a
// *FieldRefusal, ErrBusinessExists.
func (s *ProviderKycService) declareBusiness(
	user *models.User, customer *models.Customer, input dto.BusinessApplicationDTO,
) (businessDeclared, error) {
	if customer.Type != models.CustomerTypeBusiness {
		return businessDeclared{}, errNotABusiness(customer)
	}
	draft, err := s.draftOf(customer.ID)
	if err != nil {
		return businessDeclared{}, err
	}
	emails, err := s.memberEmails(customer.ID)
	if err != nil {
		return businessDeclared{}, err
	}
	if refusal := checkBusinessApplication(input, draft.TaxIdentity.Country, emails); refusal != nil {
		return businessDeclared{}, refusal
	}
	held, err := s.customers.TaxIDHeldByAnother(repositories.BusinessTaxIDDigest(draft.TaxIdentity, draft.TaxID), customer.ID)
	if err != nil {
		return businessDeclared{}, fmt.Errorf("kyc: business %d: is its document held: %w", customer.ID, err)
	}
	if held {
		return businessDeclared{}, ErrBusinessExists
	}
	profile := businessDeclaration(input, draft, time.Now().UTC().Format(time.RFC3339))
	people := businessPeople(input, user.ID)
	return businessDeclared{profile: profile, people: people, app: businessApplication(profile, people, nil)}, nil
}

// keepBusiness writes the company and its people, the declaration every later
// call reads: the documents' people, the rails' company.
func (s *ProviderKycService) keepBusiness(customerID uint, declared businessDeclared) error {
	if err := s.businesses.SaveApplication(customerID, declared.profile, declared.people); err != nil {
		return fmt.Errorf("kyc: keep business %d: %w", customerID, err)
	}
	return nil
}

// businessDocumentsOf is input as the providers receive it, checked against
// the people the company's last application named.
func (s *ProviderKycService) businessDocumentsOf(customer *models.Customer, input dto.BusinessDocumentsDTO) (ramp.Documents, error) {
	if customer.Type != models.CustomerTypeBusiness {
		return ramp.Documents{}, errNotABusiness(customer)
	}
	people, err := s.businesses.ListPeople(customer.ID)
	if err != nil {
		return ramp.Documents{}, fmt.Errorf("kyc: business %d: read its people: %w", customer.ID, err)
	}
	return businessDocuments(input, people, nil)
}

// businessProviders is fanOutProviders for the company customerID declared:
// the enabled providers taking companies that serve it. None enabled taking
// companies is ramp.ErrNoProviderAvailable, not the jurisdiction refusal the
// person-only providers would otherwise give (contract §5.4).
func (s *ProviderKycService) businessProviders(ctx context.Context, customerID uint) ([]ramp.ProviderAdapter, error) {
	who, err := s.persons.RetainedPerson(customerID)
	if err != nil {
		return nil, err
	}
	providers, err := s.fanOutProviders(ctx, who)
	if errors.Is(err, ErrJurisdictionNotServed) && !s.anyTakesCompanies() {
		return nil, ramp.ErrNoProviderAvailable
	}
	return providers, err
}

// anyTakesCompanies reports whether an enabled provider takes companies.
func (s *ProviderKycService) anyTakesCompanies() bool {
	adapters, err := implementedProviders(s.config, s.factory, s.appEnv, nil)
	if err != nil {
		return false
	}
	for _, adapter := range adapters {
		if _, takes := ramp.BusinessKycOf(adapter); takes {
			return true
		}
	}
	return false
}

// draftOf is the company customerID declared when its draft was opened.
func (s *ProviderKycService) draftOf(customerID uint) (models.BusinessProfile, error) {
	profiles, err := s.businesses.FindByCustomers([]uint{customerID})
	if err != nil {
		return models.BusinessProfile{}, fmt.Errorf("kyc: read business %d: %w", customerID, err)
	}
	draft, ok := profiles[customerID]
	if !ok {
		return models.BusinessProfile{}, fmt.Errorf("kyc: business %d declared no company", customerID)
	}
	return draft, nil
}

// businessOverall is overall for a business draft: refused, tax_id_in_use —
// the terminal category of a document someone else holds (contract §7.3) —
// when another business past draft holds its document, a rival's activation
// having outran it, whatever the provider says; and, approved, reconciled
// with its status (reconcileDraft). A person's, an active or a closed
// business's overall is left as computed.
func (s *ProviderKycService) businessOverall(customerID uint, overall Overall) (Overall, error) {
	if s.customers == nil {
		return overall, nil
	}
	customer, err := s.customers.FindByID(customerID)
	if err != nil {
		return overall, fmt.Errorf("read customer %d: %w", customerID, err)
	}
	if customer == nil || customer.Type != models.CustomerTypeBusiness || customer.Status != models.CustomerStatusDraft {
		return overall, nil
	}
	draft, err := s.draftOf(customerID)
	if err != nil {
		return overall, err
	}
	held, err := s.customers.TaxIDHeldByAnother(repositories.BusinessTaxIDDigest(draft.TaxIdentity, draft.TaxID), customerID)
	if err != nil {
		return overall, fmt.Errorf("business %d: is its document held: %w", customerID, err)
	}
	if held {
		return Overall{Status: OverallRejected, Categories: []string{models.KycRejectionTaxIDInUse}}, nil
	}
	if overall.Status == OverallApproved {
		s.reconcileDraft(customerID)
	}
	return overall, nil
}

// reconcile is reconcileDraft for a business row a refresh read approved,
// whatever the provider changed since: the verdict may have been told while
// the activation failed, and no later change would be.
func (s *ProviderKycService) reconcile(row *models.CustomerProviderKyc) {
	if row.Business && row.KycStatus == models.KycStatusApproved {
		s.reconcileDraft(row.CustomerID)
	}
}

// reconcileDraft asks the activator to activate an approved business draft.
// A failure logs: the read answers what it read, and the next one asks again.
func (s *ProviderKycService) reconcileDraft(customerID uint) {
	if s.activator == nil {
		return
	}
	if _, err := s.activator.ActivateIfApproved(customerID); err != nil {
		facades.Log().Errorf("kyc: reconcile business %d: %v", customerID, err)
	}
}

// legalNameOf is the legal name customerID declared: its last application's,
// or its draft's before one.
func (s *ProviderKycService) legalNameOf(customerID uint) (string, error) {
	draft, err := s.draftOf(customerID)
	if err != nil {
		return "", err
	}
	return draft.LegalName, nil
}

// memberEmails is the account e-mail of everyone acting for customerID.
func (s *ProviderKycService) memberEmails(customerID uint) ([]string, error) {
	members, err := s.members.MembersOf(customerID)
	if err != nil {
		return nil, fmt.Errorf("kyc: business %d: read its members: %w", customerID, err)
	}
	emails := make([]string, 0, len(members))
	for _, id := range members {
		user, err := s.users.FindByID(id)
		if err != nil {
			return nil, fmt.Errorf("kyc: business %d: read member %d: %w", customerID, id, err)
		}
		if user != nil {
			emails = append(emails, user.Email)
		}
	}
	return emails, nil
}
