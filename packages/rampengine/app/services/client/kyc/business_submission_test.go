package kyc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// businessAdapter is the harness's provider taking companies, as Bridge does.
type businessAdapter struct{ *mocks.MockProviderAdapter }

func (businessAdapter) BusinessApplicationDocuments(string, bool) []string {
	return []string{ramp.CompanyDocumentFormation}
}

// acme is the draft the company's member opened: a CNPJ, no more.
var acme = models.BusinessProfile{
	TaxIdentity: models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, TaxID: "11222333000181",
	LegalName: "ACME (RECEITA)",
}

// companyHarness is the harness acting for the company, whose one member is
// the sender, at a Bridge that takes companies.
type companyHarness struct {
	*harness
	businesses *mocks.MockCustomerBusinessProfileRepository
	customers  *mocks.MockCustomerRepository
	users      *mocks.MockUserRepository
	activator  *fakeActivator
}

// fakeActivator records the businesses a read asked to reconcile.
type fakeActivator struct{ asked []uint }

func (a *fakeActivator) ActivateIfApproved(customerID uint) (bool, error) {
	a.asked = append(a.asked, customerID)
	return true, nil
}

func newCompanyHarness(t *testing.T, takesCompanies bool) *companyHarness {
	t.Helper()
	h := newHarness(t)
	// The document's digest is keyed by APP_KEY.
	facades := mock.Factory()
	facades.Log()
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()
	c := &companyHarness{harness: h,
		businesses: mocks.NewMockCustomerBusinessProfileRepository(t),
		customers:  mocks.NewMockCustomerRepository(t),
		users:      mocks.NewMockUserRepository(t),
		activator:  &fakeActivator{},
	}
	c.customers.EXPECT().FindByID(uint(company)).Return(businessCustomer(), nil).Maybe()
	h.profileRepo.EXPECT().Find(uint(company)).Return(nil, nil).Maybe()
	persons := mocks.NewMockBusinessPersons(t)
	persons.EXPECT().BusinessPerson(uint(company)).
		Return(&ramp.Person{Identity: acme.TaxIdentity, Residence: "BRA"}, nil).Maybe()
	members := mocks.NewMockCustomerMemberRepository(t)
	members.EXPECT().MembersOf(uint(company)).Return([]uint{7}, nil).Maybe()
	c.users.EXPECT().FindByID(uint(7)).Return(h.user, nil).Maybe()
	c.businesses.EXPECT().FindByCustomers([]uint{company}).Return(map[uint]models.BusinessProfile{company: acme}, nil).Maybe()
	var provider ramp.ProviderAdapter = h.adapter
	if takesCompanies {
		provider = businessAdapter{h.adapter}
	}
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(provider), Config: ramp.NewConfig(bridgeAloneEnabled(t)), Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Integrations: h.integRepo, Staging: h.staging,
		Persons:    ramp.Persons{Customers: c.customers, Profiles: h.profileRepo, Businesses: persons},
		Members:    members,
		Businesses: c.businesses, Customers: c.customers, Users: c.users, Activator: c.activator,
		Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
	})
	return c
}

// nobodyElseHoldsIt answers that no business past draft holds acme's CNPJ.
func (c *companyHarness) nobodyElseHoldsIt() {
	digest := repositories.BusinessTaxIDDigest(acme.TaxIdentity, acme.TaxID)
	c.customers.EXPECT().TaxIDHeldByAnother(digest, uint(company)).Return(false, nil).Once()
}

// expectStaged expects one submission of kind staged for the company at
// Bridge, the row already holding the terms.
func (c *companyHarness) expectStaged(kind string, staged func(kyc.StagedSubmission) bool) {
	link := linkedIntegration("")
	var links []models.CustomerProviderIntegration
	if kind == kyc.KindDocuments {
		// Documents go to a customer the application created.
		link = companyLink()
		links = append(links, *link)
	}
	c.kycRepo.EXPECT().ListByCustomer(uint(company)).Return(nil, nil).Maybe()
	c.integRepo.EXPECT().ListByCustomer(uint(company)).Return(links, nil).Maybe()
	c.linker.EXPECT().Start(testifymock.Anything, uint(company), models.ProviderBridge).Return(link, nil).Once()
	row := companyRow(models.KycStatusNotStarted, nil)
	agreement := "sa_company"
	row.SignedAgreementID = &agreement
	c.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil)
	c.staging.EXPECT().InFlightKind(uint(company), models.ProviderBridge).Return("").Maybe()
	c.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kind && s.CustomerID == company && s.UserID == 7 && staged(s)
	})).Return("stg_1", nil).Once()
	c.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	c.dispatcher.EXPECT().Dispatch("stg_1", uint(company), models.ProviderBridge).Return(nil).Once()
}

func companyApplication() dto.BusinessApplicationDTO {
	address := dto.AddressDTO{StreetLine1: "Rua Augusta", Number: "500", City: "São Paulo", Subdivision: "SP",
		PostalCode: "01304000", Country: "BRA"}
	no := false
	return dto.BusinessApplicationDTO{
		LegalName: "ACME TECNOLOGIA LTDA", CompanyType: "ltda", IncorporationDate: "2019-03-14",
		Email: "financeiro@acme.com.br", Phone: "+5511987654321", Industry: "software_technology",
		Description: "Desenvolvimento de software", RegisteredAddress: address,
		AccountPurpose: "receive_from_customers", SourceOfFunds: "sales",
		AnnualRevenueBand: "100000_999999", MonthlyVolumeBand: "10000_49999",
		RegulatedActivity: &no, ConductsMoneyServices: &no,
		AssociatedPersons: []dto.AssociatedPersonDTO{
			{LocalRef: "ref-ana", FromMember: true, Roles: []string{"owner", "control", "signer"},
				OwnershipPercent: "60", Title: "Sócia-administradora", FirstName: "Ana", LastName: "Ribeiro",
				Email: "ana@exemplo.com", Phone: "+5511912345678", BirthDate: "1988-05-20", TaxID: "39053344705",
				TaxIDType: "cpf", TaxIDCountry: "BRA", Address: address},
			{LocalRef: "ref-bruno", Roles: []string{"owner"}, OwnershipPercent: "40",
				FirstName: "Bruno", LastName: "Costa", Email: "bruno@exemplo.com", Phone: "+5511923456789",
				BirthDate: "1990-01-02", TaxID: "52998224725", TaxIDType: "cpf", TaxIDCountry: "BRA", Address: address},
		},
		OwnershipAttested: true,
	}
}

// The company's application is kept — the company as the application states
// it on the draft's document, its people, the representative linked to the
// member's own customer — before any provider is asked, then staged at the
// providers serving the company as a business application. It carries no
// person's e-mail: the member's account e-mail is no check of it (contract
// §5.4).
func TestABusinessApplicationIsKeptThenStagedForTheCompany(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.nobodyElseHoldsIt()
	c.businesses.EXPECT().SaveApplication(uint(company),
		testifymock.MatchedBy(func(p models.BusinessProfile) bool {
			return p.TaxID == acme.TaxID && p.LegalName == "ACME TECNOLOGIA LTDA" && p.OwnershipAttestedAt != ""
		}),
		testifymock.MatchedBy(func(people []models.BusinessPerson) bool {
			return len(people) == 2 && people[0].IndividualCustomerID != nil && *people[0].IndividualCustomerID == 7 &&
				people[1].IndividualCustomerID == nil
		})).Return(nil).Once()
	c.expectStaged(kyc.KindApplication, func(s kyc.StagedSubmission) bool {
		app := s.Application
		return app != nil && app.Type == models.CustomerTypeBusiness && app.Business != nil &&
			app.Business.LegalName == "ACME TECNOLOGIA LTDA" && len(app.AssociatedPersons) == 2 &&
			app.AssociatedPersons[0].Representative && app.SignedAgreementID == "sa_company"
	})

	outcomes, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), companyApplication())

	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.True(t, outcomes[0].Accepted())
}

// What the form request cannot see is refused before anything is kept or
// asked: the company e-mail a member's, here.
func TestABusinessApplicationRefusedOnItsFieldsKeepsNothing(t *testing.T) {
	c := newCompanyHarness(t, true)
	in := companyApplication()
	in.Email = "LUIZA@example.com"

	_, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), in)

	var refusal *kyc.FieldRefusal
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, kyc.RefusalValidationFailed, refusal.Code)
	require.Contains(t, refusal.Fields["email"], "distinct_from_members")
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	c.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// A document another business past draft took since the draft was opened is
// business_exists at every submit, before anything is kept.
func TestABusinessApplicationForADocumentAnotherCompanyHoldsIsRefused(t *testing.T) {
	c := newCompanyHarness(t, true)
	digest := repositories.BusinessTaxIDDigest(acme.TaxIdentity, acme.TaxID)
	c.customers.EXPECT().TaxIDHeldByAnother(digest, uint(company)).Return(true, nil).Once()

	_, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), companyApplication())

	require.ErrorIs(t, err, kyc.ErrBusinessExists)
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The company's form serves a business only: an individual reaching it is
// refused before anything is checked.
func TestTheCompanysFormRefusesAnIndividual(t *testing.T) {
	c := newCompanyHarness(t, true)
	person := &models.Customer{Model: orm.Model{ID: 7}, Type: models.CustomerTypeIndividual}

	_, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, person, companyApplication())
	require.Error(t, err)
	_, err = c.svc.SubmitBusinessApplication(context.Background(), c.user, person, models.ProviderBridge, companyApplication())
	require.Error(t, err)
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A provider that takes no company is no provider for it: naming it is
// no_provider_available (contract §5.1), and so is a fan-out with none
// enabled that takes companies.
func TestABusinessApplicationToAProviderTakingNoCompanyIsRefused(t *testing.T) {
	c := newCompanyHarness(t, false)
	c.nobodyElseHoldsIt()

	_, err := c.svc.SubmitBusinessApplication(context.Background(), c.user, businessCustomer(), models.ProviderBridge, companyApplication())
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	_, err = c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), companyApplication())
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// The documents go to the people the last application named, by local_ref,
// and the company's by purpose; the ids Bridge gave the people are the
// worker's to place.
func TestBusinessDocumentsAreStagedForThePeopleTheApplicationNamed(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.businesses.EXPECT().ListPeople(uint(company)).Return([]models.BusinessPerson{
		{LocalRef: "ref-ana", Person: models.KycProfile{TaxID: "39053344705", TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA"}},
	}, nil).Once()
	c.expectStaged(kyc.KindDocuments, func(s kyc.StagedSubmission) bool {
		docs := s.Documents
		return docs != nil && len(docs.CompanyDocuments) == 1 && docs.CompanyDocuments[0].Purpose == "formation" &&
			len(docs.PersonDocuments) == 1 && docs.PersonDocuments[0].LocalRef == "ref-ana" &&
			docs.PersonDocuments[0].ExternalID == ""
	})

	outcomes, err := c.svc.SubmitBusinessDocumentsToEnabled(context.Background(), c.user, businessCustomer(), dto.BusinessDocumentsDTO{
		CompanyDocuments: []dto.CompanyDocumentDTO{{Purpose: "formation", File: "data:image/jpeg;base64,AAAA"}},
		PersonDocuments: []dto.PersonDocumentDTO{{LocalRef: "ref-ana", DocumentType: "national_id", IssuingCountry: "BRA",
			ImageFront: "data:image/jpeg;base64,BBBB"}},
	})

	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.True(t, outcomes[0].Accepted())
}

// A person the last application did not name is refused in the validation
// shape before anything is staged.
func TestBusinessDocumentsForAPersonNobodyNamedAreRefused(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.businesses.EXPECT().ListPeople(uint(company)).Return(nil, nil).Once()

	_, err := c.svc.SubmitBusinessDocumentsToEnabled(context.Background(), c.user, businessCustomer(), dto.BusinessDocumentsDTO{
		PersonDocuments: []dto.PersonDocumentDTO{{LocalRef: "ref-nobody", DocumentType: "national_id",
			ImageFront: "data:image/jpeg;base64,BBBB"}},
	})

	var refusal *kyc.FieldRefusal
	require.True(t, errors.As(err, &refusal), err)
	require.Contains(t, refusal.Fields["person_identity_documents.0.local_ref"], "exists")
	c.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// The hosted page, a fallback, opens Bridge's business flow for a company,
// named by its legal name unless the body names it (contract §5.1).
func TestACompanysHostedLinkIsABusinessOne(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.linker.EXPECT().Start(testifymock.Anything, uint(company), models.ProviderBridge).Return(companyLink(), nil).Once()
	c.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).
		Return(companyRow(models.KycStatusNotStarted, nil), nil)
	c.adapter.EXPECT().CreateHostedKycLink(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(o ramp.HostedLinkOpts) bool {
			return o.Type == models.CustomerTypeBusiness && o.FullName == acme.LegalName
		})).Return(&ramp.HostedKycLink{LinkID: "kyc_link_acme", URL: "https://bridge.xyz/kyc/acme"}, nil).Once()
	c.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Maybe()
	c.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()

	_, err := c.svc.HostedLink(context.Background(), c.user, company, models.ProviderBridge, dto.HostedLinkDTO{})

	require.NoError(t, err)
}

// expectApprovedAtBridge has the company's row approved at Bridge, its Pix
// endorsement granted, nothing in flight.
func (c *companyHarness) expectApprovedAtBridge(t *testing.T) {
	row := companyRow(models.KycStatusApproved, nil)
	require.NoError(t, row.SetEndorsements([]models.Endorsement{{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved}}))
	c.kycRepo.EXPECT().ListByCustomer(uint(company)).Return([]models.CustomerProviderKyc{*row}, nil).Maybe()
	c.integRepo.EXPECT().ListByCustomer(uint(company)).Return([]models.CustomerProviderIntegration{*companyLink()}, nil).Maybe()
	c.staging.EXPECT().InFlightKind(uint(company), models.ProviderBridge).Return("").Maybe()
}

// A draft whose document another business took past draft — its activation
// refused when its own approval came — reads refused, tax_id_in_use, the
// one terminal category for a document someone else holds (contract §7.3):
// whatever the provider says, it opens nothing.
func TestTheOverviewOfADraftWhoseDocumentAnotherTookIsRefused(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.expectApprovedAtBridge(t)
	digest := repositories.BusinessTaxIDDigest(acme.TaxIdentity, acme.TaxID)
	c.customers.EXPECT().TaxIDHeldByAnother(digest, uint(company)).Return(true, nil).Twice()

	view, err := c.svc.Overview(company)

	require.NoError(t, err)
	require.Equal(t, kyc.Overall{Status: kyc.OverallRejected, Categories: []string{models.KycRejectionTaxIDInUse}}, view.Overall)
	require.ErrorIs(t, c.svc.RequireApproved(company), ramp.ErrKycRejected)
}

// The same draft, its document nobody else's, reads as the provider says.
func TestTheOverviewOfADraftWhoseDocumentIsItsOwnIsTheProviders(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.expectApprovedAtBridge(t)
	c.nobodyElseHoldsIt()

	view, err := c.svc.Overview(company)

	require.NoError(t, err)
	require.Equal(t, kyc.OverallApproved, view.Overall.Status)
}

// A read of an approved draft reconciles its status (round-2 1): its
// activation may have failed when the verdict came, or the document a rival
// held may have been released since — no provider change would ever be told
// again to activate it.
func TestTheOverviewOfAnApprovedDraftReconcilesItsStatus(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.expectApprovedAtBridge(t)
	c.nobodyElseHoldsIt()

	view, err := c.svc.Overview(company)

	require.NoError(t, err)
	require.Equal(t, kyc.OverallApproved, view.Overall.Status)
	require.Equal(t, []uint{company}, c.activator.asked)
}

// A refused draft, or one not approved yet, is not reconciled.
func TestTheOverviewReconcilesNoDraftItShouldNot(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.expectApprovedAtBridge(t)
	digest := repositories.BusinessTaxIDDigest(acme.TaxIdentity, acme.TaxID)
	c.customers.EXPECT().TaxIDHeldByAnother(digest, uint(company)).Return(true, nil).Once()

	_, err := c.svc.Overview(company)

	require.NoError(t, err)
	require.Empty(t, c.activator.asked)
}

// A refresh that reads a business approved reconciles it too, whether or not
// the provider changed anything since.
func TestARefreshOfAnApprovedBusinessReconcilesItsStatus(t *testing.T) {
	c := newCompanyHarness(t, true)
	link := companyLink()
	c.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(link, nil).Maybe()
	row := companyRow(models.KycStatusApproved, nil)
	c.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil).Maybe()
	c.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusApproved}, nil).Once()
	c.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Maybe()
	c.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()

	_, err := c.svc.Refresh(context.Background(), company, models.ProviderBridge)

	require.NoError(t, err)
	require.Equal(t, []uint{company}, c.activator.asked)
}

// A second application while the first is still on its way answers
// submission_in_flight and keeps nothing: the company and its people stay
// as the submission in flight declared them (round-2 review, item 3).
func TestABusinessApplicationBehindOneInFlightKeepsNothing(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.nobodyElseHoldsIt()
	c.staging.EXPECT().InFlightKind(uint(company), models.ProviderBridge).Return(kyc.KindApplication).Maybe()
	c.kycRepo.EXPECT().ListByCustomer(uint(company)).Return(nil, nil).Maybe()
	c.integRepo.EXPECT().ListByCustomer(uint(company)).Return(nil, nil).Maybe()

	outcomes, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), companyApplication())

	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	require.Len(t, outcomes, 1)
	require.ErrorIs(t, outcomes[0].Err, kyc.ErrSubmissionInFlight)
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)

	digest := repositories.BusinessTaxIDDigest(acme.TaxIdentity, acme.TaxID)
	c.customers.EXPECT().TaxIDHeldByAnother(digest, uint(company)).Return(false, nil).Once()
	_, err = c.svc.SubmitBusinessApplication(context.Background(), c.user, businessCustomer(), models.ProviderBridge, companyApplication())
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// An application every provider passes over — approved, owing nothing an
// application carries — keeps nothing either: the declaration is kept for
// a submission some provider receives.
func TestABusinessApplicationNoProviderReceivesKeepsNothing(t *testing.T) {
	c := newCompanyHarness(t, true)
	c.nobodyElseHoldsIt()
	c.staging.EXPECT().InFlightKind(uint(company), models.ProviderBridge).Return("").Maybe()
	approved := companyRow(models.KycStatusApproved, nil)
	c.kycRepo.EXPECT().ListByCustomer(uint(company)).Return([]models.CustomerProviderKyc{*approved}, nil).Maybe()
	c.integRepo.EXPECT().ListByCustomer(uint(company)).Return([]models.CustomerProviderIntegration{*companyLink()}, nil).Maybe()

	outcomes, err := c.svc.SubmitBusinessApplicationToEnabled(context.Background(), c.user, businessCustomer(), companyApplication())

	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.True(t, outcomes[0].Skipped)
	c.businesses.AssertNotCalled(t, "SaveApplication", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}
