package kyc_test

import (
	"context"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// kybSteps is every PJ step, in wizard order: what a draft nobody sent owes.
var kybSteps = []string{"company", "company_address", "activity", "owners", "company_documents", "terms"}

// A draft nothing was sent for owes the KYB wizard, every step of it: the
// row a provider the business never touched is synthesized as is the
// company's, not a person's (API contract §7.1).
func TestTheOverviewOfANeverSubmittedDraftOwesEveryPJStep(t *testing.T) {
	h := newHarness(t)
	h.withBusinessDeclared(t, &keyedLocker{})
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
	h.kycRepo.EXPECT().ListByCustomer(uint(company)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(company)).Return(nil, nil).Once()
	h.staging.EXPECT().InFlightKind(uint(company), models.ProviderBridge).Return("").Once()

	view, err := h.svc.Overview(company)
	require.NoError(t, err)

	require.Equal(t, kyc.OverallNotStarted, view.Overall.Status)
	require.Equal(t, kybSteps, view.Overall.PendingSteps)
	require.Empty(t, view.Overall.PendingDocuments)
	require.Len(t, view.Providers, 1)
	require.True(t, view.Providers[0].Business)
	require.Equal(t, kybSteps, kyc.PendingStepsFor(&view.Providers[0]))
}

// One provider's row, read alone — the per-provider wizard, the pending steps
// a refusal carries — is the company's too before anything was stored.
func TestABusinesssUntouchedProviderRowIsTheCompanys(t *testing.T) {
	h := newHarness(t)
	h.withBusinessDeclared(t, &keyedLocker{})
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(nil, nil).Twice()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(nil, nil).Twice()

	row, err := h.svc.Get(company, models.ProviderBridge)
	require.NoError(t, err)
	require.True(t, row.Business)
	require.Equal(t, kybSteps, h.svc.PendingSteps(company, models.ProviderBridge))
}

// The provider's answer for a business is read in the KYB's words: what
// Bridge names a company is missing opens the PJ steps that collect it.
func TestABusinesssRequirementsAreThePJSteps(t *testing.T) {
	h := newHarness(t)
	h.withBusinessDeclared(t, &keyedLocker{})
	link := linkedIntegration("cus_acme")
	link.CustomerID = company
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(link, nil).Once()
	h.adapter.EXPECT().GetRequirements(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycRequirements{
			Status:  models.KycStatusIncomplete,
			Missing: []string{"proof_of_address", "government_id_document"},
		}, nil).Once()

	got, err := h.svc.Requirements(context.Background(), company, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, []string{"owners", "company_documents"}, got.PendingSteps)
}

// companyRow is the company's row at Bridge in status, holding the ids Bridge
// gave the people of its last application.
func companyRow(status string, people map[string]string) *models.CustomerProviderKyc {
	row := kycRow(status)
	row.CustomerID, row.Business = company, true
	if people != nil {
		row.SetPersonIDs(people)
	}
	return row
}

func companyLink() *models.CustomerProviderIntegration {
	link := linkedIntegration("cus_company")
	link.CustomerID = company
	return link
}

// The documents follow the application's 202 before Bridge has answered it,
// so no id for the people exists when they are staged: the worker places each
// person's documents by the id the row holds when it sends them.
func TestTheWorkerPlacesAPersonsDocumentsByTheIDTheRowHolds(t *testing.T) {
	h := newHarness(t)
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		CustomerID: company, UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
		PreviousStatus: models.KycStatusSubmitting,
		Documents:      &ramp.Documents{PersonDocuments: []ramp.PersonDocuments{{LocalRef: "ref-ana"}}},
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(companyLink(), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).
		Return(companyRow(models.KycStatusRequiresInfo, map[string]string{"ref-ana": "ap_ana"}), nil).Once()
	h.adapter.EXPECT().SubmitDocuments(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(d ramp.Documents) bool {
			return len(d.PersonDocuments) == 1 && d.PersonDocuments[0].ExternalID == "ap_ana"
		})).Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", company, models.ProviderBridge))
}

// A resubmission names each person Bridge already knows by its id, so Bridge
// updates them rather than creating them again.
func TestTheWorkerNamesThePeopleBridgeKnowsInAResubmission(t *testing.T) {
	h := newHarness(t)
	app := ramp.Application{Type: models.CustomerTypeBusiness, Business: &models.BusinessProfile{},
		AssociatedPersons: []ramp.AssociatedPerson{{LocalRef: "ref-ana"}, {LocalRef: "ref-carla"}}}
	h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
		CustomerID: company, UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindApplication,
		PreviousStatus: models.KycStatusRequiresInfo, Application: &app,
	}, nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(companyLink(), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).
		Return(companyRow(models.KycStatusSubmitting, map[string]string{"ref-ana": "ap_ana", "ref-bruno": "ap_bruno"}), nil).Once()
	h.adapter.EXPECT().SubmitApplication(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(a ramp.Application) bool {
			return a.AssociatedPersons[0].ExternalID == "ap_ana" && a.AssociatedPersons[1].ExternalID == ""
		})).Return(&ramp.KycState{Status: models.KycStatusUnderReview}, nil).Once()
	h.staging.EXPECT().Forget("stg_1").Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()

	require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", company, models.ProviderBridge))
	require.Empty(t, app.AssociatedPersons[0].ExternalID, "the staged application is left as it was staged")
}

// Documents for a person Bridge has no id for yet, behind an application
// still on its way, wait for it as documents wait for the customer; with no
// application coming they are not sent — Bridge would refuse the whole call —
// and the row says why.
func TestTheWorkerHoldsAPersonsDocumentsUntilTheApplicationNamesThem(t *testing.T) {
	for name, tc := range map[string]struct {
		previous, current string
		requeued          bool
	}{
		"behind an application in flight": {models.KycStatusSubmitting, models.KycStatusSubmitting, true},
		"no application coming":           {models.KycStatusRequiresInfo, models.KycStatusSubmitting, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithQueue(t, "redis")
			h.staging.EXPECT().Get("stg_1").Return(&kyc.StagedSubmission{
				CustomerID: company, UserID: 7, Provider: models.ProviderBridge, Kind: kyc.KindDocuments,
				PreviousStatus: tc.previous,
				Documents:      &ramp.Documents{PersonDocuments: []ramp.PersonDocuments{{LocalRef: "ref-carla"}}},
			}, nil).Once()
			h.integRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).Return(companyLink(), nil).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(company), models.ProviderBridge).
				Return(companyRow(tc.current, map[string]string{"ref-ana": "ap_ana"}), nil).Once()
			if tc.requeued {
				h.dispatcher.EXPECT().DispatchAfter("stg_1", uint(company), models.ProviderBridge, testifymock.Anything).Return(nil).Once()
			} else {
				h.staging.EXPECT().Forget("stg_1").Once()
				h.kycRepo.EXPECT().UpdateIfStatus(testifymock.MatchedBy(func(row *models.CustomerProviderKyc) bool {
					return row.LastErrorCode != nil
				}), testifymock.Anything).Return(true, nil).Once()
				h.broadcaster.EXPECT().PublishKyc(testifymock.Anything, testifymock.Anything).Maybe()
			}

			require.NoError(t, h.svc.ProcessStagedSubmission("stg_1", company, models.ProviderBridge))
			h.adapter.AssertNotCalled(t, "SubmitDocuments", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}
