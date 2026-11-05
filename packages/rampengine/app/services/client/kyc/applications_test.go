package kyc_test

import (
	"context"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// A provider owes the user's whole application exactly when it holds no
// customer for them and nothing is on its way to create one. User 2 in
// production (24/09/2026) is the case: KYC'd at Ozav before Bridge was
// enabled, so Bridge had a row, no customer and no named requirement — it
// read "nothing owed" and the app never sent it an application.
func TestNeedsApplication(t *testing.T) {
	cases := []struct {
		status              string
		hasExternalCustomer bool
		want                bool
	}{
		{models.KycStatusNotStarted, false, true},
		{models.KycStatusIncomplete, false, true},
		{models.KycStatusRequiresInfo, false, true},
		{models.KycStatusRejected, false, true},
		// An application is in flight: asking again would stage a second one.
		{models.KycStatusSubmitting, false, false},
		// Reviewing, decided or suspended: a customer exists upstream even if
		// the link has not caught up; a fresh application would start over.
		{models.KycStatusUnderReview, false, false},
		{models.KycStatusApproved, false, false},
		{models.KycStatusPaused, false, false},
		// A customer exists: what is missing is named in its requirements.
		{models.KycStatusIncomplete, true, false},
		{models.KycStatusNotStarted, true, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, kyc.NeedsApplication(tc.status, tc.hasExternalCustomer),
			"status=%s hasExternalCustomer=%v", tc.status, tc.hasExternalCustomer)
	}
}

func TestPendingStepsForAddsTheWholeApplicationWhenOneIsOwed(t *testing.T) {
	row := providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address")
	row.NeedsApplication = true

	require.Equal(t,
		[]string{"personal", "profile", "address", "proof_of_address", "document", "selfie"},
		kyc.PendingStepsFor(&row))
}

// A provider that asks every customer for a document declares it, and a user
// who owes it the whole application is asked for that document in the same
// pass rather than one round trip later (decided with Luiz on 28/09/2026 for
// Ozav's bank statement).
func TestPendingStepsForAddsTheDocumentsTheProviderWantsWithAnApplication(t *testing.T) {
	row := providerState(models.ProviderOzav, models.KycStatusNotStarted)
	row.NeedsApplication = true
	row.ApplicationDocuments = []string{"bank_statement"}

	require.Equal(t,
		[]string{"personal", "profile", "address", "bank_statement", "document", "selfie"},
		kyc.PendingStepsFor(&row))
}

// Once the customer exists the provider names what it still wants itself; the
// declaration is only for the pass that creates it.
func TestPendingStepsForIgnoresApplicationDocumentsWhenNoApplicationIsOwed(t *testing.T) {
	row := providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address")
	row.ApplicationDocuments = []string{"bank_statement"}

	require.Equal(t, []string{"proof_of_address"}, kyc.PendingStepsFor(&row))
}

func TestPendingStepsForIsTheProvidersOwnListOtherwise(t *testing.T) {
	row := providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address")

	require.Equal(t, []string{"proof_of_address"}, kyc.PendingStepsFor(&row))
}

func TestMarkNeedsApplicationReadsWhoHoldsACustomer(t *testing.T) {
	rows := []models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusIncomplete),
		providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
	}
	ozav := linkedIntegration("ozav_cus_1")
	ozav.Provider = models.ProviderOzav
	// Bridge's link exists but carries no customer id: the integration was
	// opened and nothing was ever created upstream.
	bridge := linkedIntegration("")

	kyc.MarkNeedsApplication(rows, []models.CustomerProviderIntegration{*ozav, *bridge}, nil)

	require.True(t, rows[0].NeedsApplication, "bridge holds no customer")
	require.False(t, rows[1].NeedsApplication, "ozav holds one")
}

func linkFor(provider, externalID string) models.CustomerProviderIntegration {
	link := linkedIntegration(externalID)
	link.Provider = provider
	return *link
}

// The listing is what the app builds its whole KYC picture from — status,
// gate, banner and wizard steps — and the submissions only ever reach enabled
// providers. A disabled provider in the listing made the app ask for Ozav's
// proof of address and send it to Bridge, which had no customer to attach it
// to.
func TestListShowsOnlyEnabledProviders(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.adapters[models.ProviderBridge].EXPECT().RequiresTos().Return(true).Maybe()
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
		providerState(models.ProviderBridge, models.KycStatusIncomplete),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderOzav, "ozav_cus_1"), linkFor(models.ProviderBridge, ""),
	}, nil).Once()

	rows, err := h.svc.List(7)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, models.ProviderBridge, rows[0].Provider)
	require.True(t, rows[0].NeedsApplication)
}

// An enabled provider the user never touched is the "admin just enabled a new
// provider" case: it has to be listed, owing the application, or the app has
// no way to know it exists.
func TestListSynthesizesAnEnabledProviderTheUserNeverTouched(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	for _, adapter := range h.adapters {
		adapter.EXPECT().RequiresTos().Return(true).Maybe()
	}
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusApproved),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderBridge, "cus_bridge"),
	}, nil).Once()

	rows, err := h.svc.List(7)
	require.NoError(t, err)
	byProvider := map[string]models.CustomerProviderKyc{}
	for _, row := range rows {
		byProvider[row.Provider] = row
	}
	require.Len(t, byProvider, 2)
	require.False(t, byProvider[models.ProviderBridge].NeedsApplication, "approved with a customer")
	require.Equal(t, models.KycStatusNotStarted, byProvider[models.ProviderOzav].KycStatus)
	require.True(t, byProvider[models.ProviderOzav].NeedsApplication, "never touched")
}

// --- fan-out filters ---------------------------------------------------------

// A provider holding a customer that is approved and owes no personal data,
// profile or address has nothing an application could satisfy; sending one
// anyway re-opens its review.
func TestSubmitApplicationToEnabledSkipsAnApprovedProviderOwingNothing(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusApproved),
		providerState(models.ProviderOzav, models.KycStatusIncomplete),
	}, models.ProviderBridge)
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	require.True(t, outcomes[0].Skipped, "bridge: approved, nothing owed")
	require.True(t, outcomes[1].Accepted(), "ozav: no customer, so this creates it")
}

// Approved, but naming a personal-data item: the application is the call that
// carries it.
func TestSubmitApplicationToEnabledUpdatesAnApprovedProviderOwingPersonalData(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedSnapshot([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusApproved, "mother_name"),
	}, models.ProviderBridge)
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
}

// User 2 in production, 24/09/2026: documents sent to a provider with no
// customer and no application on its way were staged, queued and dropped by
// the worker — the row went back to `incomplete` saying nothing. They are not
// sent at all now; the provider that can use them still gets them.
func TestSubmitDocumentsToEnabledSkipsAProviderWithNoCustomerAndNoApplication(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusIncomplete),
		providerState(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
	}, models.ProviderOzav)
	h.expectAcceptedDocumentsSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	require.True(t, outcomes[0].Skipped, "bridge has nothing to attach them to")
	require.True(t, outcomes[1].Accepted())
}

// The same pass of the wizard: the application went first and is creating the
// customer, so the documents queue behind it.
func TestSubmitDocumentsToEnabledFollowsAnApplicationInFlight(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedSnapshot([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusSubmitting),
	})
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
}

// Decided on 24/09/2026: the CPF is retained with the rest of the profile
// (encrypted like it), so a provider enabled later asks only for the photos.
// With the country it was declared under, which is what says it is a CPF.
func TestSubmitApplicationToEnabledRetainsTheTaxID(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.MatchedBy(func(profile ramp.Profile) bool {
		return profile.TaxID == "39053344705" && profile.TaxIDCountry == "BRA"
	})).Return(nil).Once()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")
	app := fullApplication()
	app.TaxID = "39053344705"
	app.Country = "BRA"

	_, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), app)
	require.NoError(t, err)
}

// A provider rejected for reasons the user can fix owes the steps those
// reasons reopen, next to whatever it still names — which is what sends it the
// resubmission of exactly those steps.
func TestPendingStepsForOwesTheStepsACorrigibleRejectionReopens(t *testing.T) {
	row := providerState(models.ProviderBridge, models.KycStatusRejected, "external_account")
	row.SetRejectionCategories([]string{models.KycRejectionDataMismatch})

	require.Equal(t, []string{"personal", "document"}, kyc.PendingStepsFor(&row))
	require.Contains(t, kyc.OutstandingFor(&row), models.KycRejectionDataMismatch)
}

// A provider that refused for good owes nothing: it never receives this
// user's data again, whatever its requirements still name, and it owes no
// application either even with no customer behind it (an Ozav create refused
// over the tax id).
func TestPendingStepsForATerminalProviderIsNothing(t *testing.T) {
	row := providerState(models.ProviderBridge, models.KycStatusRejected, "live_selfie")
	row.SetRejectionCategories([]string{models.KycRejectionSelfieMismatch, models.KycRejectionTaxIDInUse})
	row.NeedsApplication = true

	require.Empty(t, kyc.PendingStepsFor(&row))
}

func TestMarkNeedsApplicationSkipsATerminalProvider(t *testing.T) {
	refused := providerState(models.ProviderOzav, models.KycStatusRejected)
	refused.SetRejectionCategories([]string{models.KycRejectionTaxIDInUse})
	rows := []models.CustomerProviderKyc{refused}

	kyc.MarkNeedsApplication(rows, nil, nil)

	require.False(t, rows[0].NeedsApplication, "a provider done with the user owes it no application")
}
