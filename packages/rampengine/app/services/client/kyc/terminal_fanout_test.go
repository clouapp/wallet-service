package kyc_test

import (
	"context"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

func rejectedState(provider string, categories ...string) models.CustomerProviderKyc {
	row := providerState(provider, models.KycStatusRejected)
	row.SetRejectionCategories(categories)
	return row
}

// withTimeline rebuilds the service over the same collaborators, recording
// the timeline entries it writes.
func (h *fanOutHarness) withTimeline(seen *[]rampsvc.Entry) {
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.registered...), Config: h.config, Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Members: h.members, Integrations: h.integRepo,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
		Timeline: recorderFunc(func(e rampsvc.Entry) { *seen = append(*seen, e) }),
	})
}

func skipReasons(seen []rampsvc.Entry) map[string]string {
	out := map[string]string{}
	for _, entry := range seen {
		if entry.SkipReason != "" {
			out[entry.Provider] = entry.SkipReason
		}
	}
	return out
}

// A provider that refused the user for good never receives their data again,
// and the timeline says why it was passed over. The provider rejected for a
// reason the user can fix gets the resubmission of the steps it asked for.
func TestDocumentsFanOutSkipsATerminalProvider(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		rejectedState(models.ProviderBridge, models.KycRejectionTaxIDInUse, models.KycRejectionSelfieMismatch),
		rejectedState(models.ProviderOzav, models.KycRejectionDocumentUnreadable),
	}, models.ProviderBridge, models.ProviderOzav)
	var seen []rampsvc.Entry
	h.withTimeline(&seen)
	h.expectAcceptedDocumentsSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	require.True(t, outcomes[0].Skipped, "bridge refused for good")
	require.True(t, outcomes[1].Accepted(), "ozav asked for a new document")
	require.Equal(t, map[string]string{models.ProviderBridge: "terminal"}, skipReasons(seen))
}

// The same for the application: a terminal provider is skipped even with no
// customer behind it — the Ozav create refused over the tax id — which would
// otherwise read as owing the whole application and be sent it again.
func TestApplicationFanOutSkipsATerminalProvider(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		rejectedState(models.ProviderBridge, models.KycRejectionDataMismatch),
		rejectedState(models.ProviderOzav, models.KycRejectionTaxIDInUse),
	}, models.ProviderBridge)
	var seen []rampsvc.Entry
	h.withTimeline(&seen)
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.Len(t, outcomes, 2)
	require.True(t, outcomes[0].Accepted(), "bridge asked for the personal data again")
	require.True(t, outcomes[1].Skipped, "ozav refused for good")
	require.Equal(t, map[string]string{models.ProviderOzav: "terminal"}, skipReasons(seen))
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, uint(7), models.ProviderOzav)
}

// The provider routes reach one provider by name, past the fan-out's filter;
// a provider done with the user refuses there too, with the code the app
// already reads as "nothing to do here", and nothing is staged.
func TestSubmitToAProviderThatRefusedForGoodIsRefused(t *testing.T) {
	terminal := func() *models.CustomerProviderKyc {
		row := kycRow(models.KycStatusRejected)
		row.SetRejectionCategories([]string{models.KycRejectionTaxIDInUse})
		return row
	}
	t.Run("application", func(t *testing.T) {
		h := newHarness(t)
		h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
			Return(linkedIntegration("cus_1"), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(terminal(), nil).Once()

		_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
		require.ErrorIs(t, err, ramp.ErrKycRejected)
		h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	})
	t.Run("documents", func(t *testing.T) {
		h := newHarness(t)
		h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
			Return(linkedIntegration("cus_1"), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(terminal(), nil).Once()

		_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, fullDocuments())
		require.ErrorIs(t, err, ramp.ErrKycRejected)
		h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
	})
}

// A rejection no adapter classified that names nothing is Terminal, and the
// fan-out reads the same classification the overview does.
func TestFanOutSkipsAnUnclassifiedRejectionWithNothingToSend(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		providerState(models.ProviderBridge, models.KycStatusRejected),
		rejectedState(models.ProviderOzav, models.KycRejectionDocumentUnreadable),
	}, models.ProviderBridge, models.ProviderOzav)
	var seen []rampsvc.Entry
	h.withTimeline(&seen)
	h.expectAcceptedDocumentsSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.True(t, outcomes[0].Skipped)
	require.Equal(t, map[string]string{models.ProviderBridge: "terminal"}, skipReasons(seen))
}

// A row stuck on submitting past the staging TTL has nothing in flight: an
// application sent now is not a duplicate, so the fan-out sends it rather
// than reading the stuck status as a review in progress.
func TestApplicationFanOutReachesAProviderStuckOnSubmitting(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	stuck := providerState(models.ProviderBridge, models.KycStatusSubmitting)
	stuck.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-kyc.StagingTTL - time.Minute)))
	h.seedSnapshot([]models.CustomerProviderKyc{stuck}, models.ProviderBridge)
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
}

// Marking reads a stuck row with no customer as owing the application the
// stuck submission never delivered.
func TestMarkNeedsApplicationReadsAStuckSubmissionAsReleased(t *testing.T) {
	stuck := providerState(models.ProviderBridge, models.KycStatusSubmitting)
	stuck.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-kyc.StagingTTL - time.Minute)))
	fresh := providerState(models.ProviderOzav, models.KycStatusSubmitting)
	fresh.UpdatedAt = carbon.NewDateTime(carbon.Now())
	rows := []models.CustomerProviderKyc{stuck, fresh}

	kyc.MarkNeedsApplication(rows, nil, nil)

	require.True(t, rows[0].NeedsApplication, "stuck: nothing is on its way to create the customer")
	require.False(t, rows[1].NeedsApplication, "in flight: the application is on its way")
}

func terminalRow() *models.CustomerProviderKyc {
	row := kycRow(models.KycStatusRejected)
	row.SetRejectionCategories([]string{models.KycRejectionTaxIDInUse})
	return row
}

// Every path that would send a terminal provider something refuses before the
// provider is called: the hosted link carries the user's name and e-mail, the
// endorsement request and the terms session speak for them.
func TestNothingReachesAProviderThatRefusedForGood(t *testing.T) {
	t.Run("hosted link", func(t *testing.T) {
		h := newHarness(t)
		h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
			Return(linkedIntegration("cus_1"), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(terminalRow(), nil).Once()

		_, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{FullName: "Luiza Souza"})
		require.ErrorIs(t, err, ramp.ErrKycRejected)
		h.adapter.AssertNotCalled(t, "CreateHostedKycLink", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	})
	t.Run("endorsements", func(t *testing.T) {
		h := newHarness(t)
		h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration("cus_1"), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(terminalRow(), nil).Once()

		_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge, []string{models.EndorsementPix})
		require.ErrorIs(t, err, ramp.ErrKycRejected)
		h.adapter.AssertNotCalled(t, "RequestEndorsements", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	})
	t.Run("terms session", func(t *testing.T) {
		h := newHarness(t)
		h.adapter.EXPECT().RequiresTos().Return(true).Once()
		h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
			Return(linkedIntegration("cus_1"), nil).Once()
		h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(terminalRow(), nil).Once()

		_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
		require.ErrorIs(t, err, ramp.ErrKycRejected)
		h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	})
}

// A refresh reads and sends nothing: it still works for a terminal provider,
// or the admin could never see a decision change.
func TestRefreshStillReadsAProviderThatRefusedForGood(t *testing.T) {
	h := newHarness(t)
	row := terminalRow()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration("cus_1"), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(&ramp.KycState{Status: models.KycStatusRejected, RejectionCategories: []string{models.KycRejectionTaxIDInUse}}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()

	_, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
}

// The terms fan-out passes a terminal provider over, even with its terms
// unsigned.
func TestTosFanOutSkipsAProviderThatRefusedForGood(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedProviderStates(
		rejectedState(models.ProviderBridge, models.KycRejectionTaxIDInUse),
		providerState(models.ProviderOzav, models.KycStatusNotStarted),
	)
	h.expectTosSession(models.ProviderOzav, &ramp.TosSession{URL: "https://ozav.example/tos"}, nil)

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{Modes: []string{kyc.TosModeHosted}})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, models.ProviderOzav, sessions[0].Provider)
	h.adapters[models.ProviderBridge].AssertNotCalled(t, "CreateTosSession",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A provider rejected for reasons the user can fix receives the resubmission
// of the steps it asked for, and only those: a selfie that did not match is not
// answered by restating the personal data, while data that did not match is.
func TestApplicationFanOutSendsOnlyTheStepsARejectionAskedFor(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.seedSnapshot([]models.CustomerProviderKyc{
		rejectedState(models.ProviderBridge, models.KycRejectionSelfieMismatch),
		rejectedState(models.ProviderOzav, models.KycRejectionPhoneInUse),
	}, models.ProviderBridge, models.ProviderOzav)
	var seen []rampsvc.Entry
	h.withTimeline(&seen)
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())
	require.NoError(t, err)
	require.True(t, outcomes[0].Skipped, "bridge asked for a document and a selfie")
	require.True(t, outcomes[1].Accepted(), "ozav asked for another phone number")
	require.Equal(t, map[string]string{models.ProviderBridge: "nothing_owed"}, skipReasons(seen))
}
