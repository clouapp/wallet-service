package kyc_test

import (
	"context"
	"testing"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// company is a business customer user 7 is a member of.
const company = 1_000_000_000_000

func businessCustomer() *models.Customer {
	return &models.Customer{Model: orm.Model{ID: company}, Type: models.CustomerTypeBusiness, Status: models.CustomerStatusDraft}
}

// keyedLocker records the lock keys asked of it.
type keyedLocker struct{ keys []string }

func (l *keyedLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) error {
	l.keys = append(l.keys, key)
	return fn(ctx)
}

// withBusinessDeclared rebuilds the service reading the company as a business
// that declared no one in particular — the business tables' answer before a
// company's identity is known — so a submission for it is served.
func (h *harness) withBusinessDeclared(t *testing.T, locker *keyedLocker) {
	t.Helper()
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(uint(company)).Return(businessCustomer(), nil).Maybe()
	businesses := mocks.NewMockBusinessPersons(t)
	businesses.EXPECT().BusinessPerson(uint(company)).Return(nil, nil).Maybe()
	h.profileRepo.EXPECT().Find(uint(company)).Return(nil, nil).Maybe()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.adapter), Config: ramp.NewConfig(bridgeAloneEnabled(t)), Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Integrations: h.integRepo, Staging: h.staging,
		Persons:    ramp.Persons{Customers: customers, Profiles: h.profileRepo, Businesses: businesses},
		Members:    h.members,
		Dispatcher: h.dispatcher, Locker: locker, Broadcaster: h.broadcaster, Notifier: h.notifier,
	})
}

// The person's form is an individual's application: a business sent through
// it is refused before anything is checked, staged or asked of a provider —
// sent, it would reach Bridge as a company described by a person's fields.
func TestTheIndividualFormRefusesABusiness(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.SubmitApplication(context.Background(), h.user, businessCustomer(), models.ProviderBridge, fullApplication())
	require.Error(t, err)
	_, err = h.svc.SubmitApplicationToEnabled(context.Background(), h.user, businessCustomer(), fullApplication())
	require.Error(t, err)

	h.profileRepo.AssertNotCalled(t, "CheckIdentityAvailable", testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The application is of the customer's type: the person's form leaves as an
// individual's, whatever the client sent.
func TestTheApplicationIsOfTheCustomersType(t *testing.T) {
	h := newHarness(t)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Application != nil && s.Application.Type == models.CustomerTypeIndividual
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(kycRow(models.KycStatusSubmitting), nil).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())

	require.NoError(t, err)
}

// What a member sends for a company is the company's: its row is read and
// marked, its lock taken, its in-flight mark and queue argument name it, and
// only the timeline names the member who sent it (spec §5, F1).
func TestDocumentsAMemberSendsForACompanyAreTheCompanys(t *testing.T) {
	h := newHarness(t)
	locker := &keyedLocker{}
	h.withBusinessDeclared(t, locker)
	row := kycRow(models.KycStatusNotStarted)
	row.CustomerID = company
	h.linker.EXPECT().Start(testifymock.Anything, uint(company), models.ProviderBridge).
		Return(linkedIntegration("cus_company"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil).Once()
	h.staging.EXPECT().Put(testifymock.MatchedBy(func(s kyc.StagedSubmission) bool {
		return s.Kind == kyc.KindDocuments && s.CustomerID == company && s.UserID == 7
	})).Return("stg_1", nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(company), models.ProviderBridge).Return(nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil).Once()

	_, err := h.svc.SubmitDocuments(context.Background(), h.user, company, models.ProviderBridge, fullDocuments())

	require.NoError(t, err)
	require.Equal(t, []string{"kyc:submit:1000000000000:bridge"}, locker.keys)
}

// The worker reads the row of the customer its job names and records the call
// for the company, by the member who sent it: provider_calls names both, the
// user who acted and the customer acted for.
func TestTheWorkerRecordsTheSenderOfACompanysSubmission(t *testing.T) {
	h := newHarnessWithQueue(t, "redis")
	var seen []rampsvc.Entry
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.adapter), Config: ramp.NewConfig(bridgeAloneEnabled(t)), Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Members: h.members, Integrations: h.integRepo, Staging: h.staging,
		Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
		Timeline: recorderFunc(func(e rampsvc.Entry) { seen = append(seen, e) }),
	})
	docs := stagedDocuments()
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		CustomerID: company, UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		PreviousStatus: models.KycStatusIncomplete, Documents: &docs,
	}, nil).Once()
	link := linkedIntegration("cus_company")
	link.CustomerID = company
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(link, nil).Once()
	row := kycRow(models.KycStatusSubmitting)
	row.CustomerID = company
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, ramp.CustomerRef{CustomerID: company, ExternalID: "cus_company"},
		testifymock.Anything).Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, models.KycStatusSubmitting).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", company, models.ProviderBridge))

	require.Len(t, seen, 1)
	require.Equal(t, uint(7), seen[0].UserID)
	require.Equal(t, uint(company), seen[0].CustomerID, "the call is the company's, which its 360 lists")
}

// A company's change at a provider is told to every user who acts for it,
// naming the company: the live stream is per user, and a member watching
// their own wallet must still see the company's verification move.
func TestACompanysChangeIsToldToEveryMember(t *testing.T) {
	h := newHarness(t)
	members := mocks.NewMockCustomerMemberRepository(t)
	members.EXPECT().MembersOf(uint(company)).Return([]uint{7, 9}, nil).Once()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.adapter), Config: ramp.NewConfig(bridgeAloneEnabled(t)), Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Integrations: h.integRepo, Members: members,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster,
		Notifier: h.notifier,
	})
	link := linkedIntegration("cus_company")
	link.CustomerID = company
	row := kycRow(models.KycStatusUnderReview)
	row.CustomerID = company
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(link, nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, ramp.CustomerRef{CustomerID: company, ExternalID: "cus_company"}).
		Return(&ramp.KycState{Status: models.KycStatusApproved}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
	namesTheCompany := testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.CustomerID == company && e.KycStatus == models.KycStatusApproved
	})
	h.broadcaster.EXPECT().PublishKyc(uint(7), namesTheCompany).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(9), namesTheCompany).Once()

	_, err := h.svc.Refresh(context.Background(), company, models.ProviderBridge)

	require.NoError(t, err)
}
