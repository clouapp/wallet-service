package ramp_test

import (
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/ozav"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func newTimeline(t *testing.T) (*rampsvc.ProviderTimeline, *mocks.MockProviderCallRepository) {
	t.Helper()
	mock.Factory().Log()
	repo := mocks.NewMockProviderCallRepository(t)
	return rampsvc.NewProviderTimeline(repo), repo
}

func TestRecordWritesASuccessfulCall(t *testing.T) {
	timeline, repo := newTimeline(t)

	var got *models.ProviderCall
	repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(c *models.ProviderCall) error {
		got = c
		return nil
	}).Once()

	timeline.Record(rampsvc.Entry{
		UserID: 7, CustomerID: 1_000_000_000_007, Provider: models.ProviderOzav,
		Operation: rampsvc.OpSubmitApplication, SubmissionID: "stg_1",
		Started: time.Now().Add(-30 * time.Millisecond),
	})

	require.NotNil(t, got)
	require.Equal(t, uint(7), got.UserID, "who acted")
	require.Equal(t, uint(1_000_000_000_007), got.CustomerID, "whom for")
	require.Equal(t, models.ProviderCallOK, got.Outcome)
	require.Nil(t, got.ErrorCode)
	require.NotNil(t, got.SubmissionID)
	require.Equal(t, "stg_1", *got.SubmissionID)
	require.NotNil(t, got.LatencyMs, "a call with a start time has a duration worth reading")
}

// The line the whole table exists for. A submission that never leaves looks
// exactly like one nobody attempted, and only this says which — on 02/09 a
// user's documents were dropped one second after the application failed, and
// the sole trace was a log line on a release directory since pruned.
func TestRecordWritesADecisionNotToCall(t *testing.T) {
	timeline, repo := newTimeline(t)

	var got *models.ProviderCall
	repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(c *models.ProviderCall) error {
		got = c
		return nil
	}).Once()

	timeline.Record(rampsvc.Entry{
		UserID: 7, Provider: models.ProviderOzav,
		Operation:  rampsvc.OpSubmitDocuments,
		SkipReason: "nothing_owed",
	})

	require.Equal(t, models.ProviderCallSkipped, got.Outcome)
	require.NotNil(t, got.ErrorCode)
	require.Equal(t, "nothing_owed", *got.ErrorCode,
		"the reason in this side's words is what makes an absence readable")
}

// The stable contract code, never the upstream message — a provider's text
// restates submitted fields often enough that it cannot sit beside a user id.
func TestRecordKeepsTheContractCodeNotTheProviderText(t *testing.T) {
	timeline, repo := newTimeline(t)

	var got *models.ProviderCall
	repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(c *models.ProviderCall) error {
		got = c
		return nil
	}).Once()

	timeline.Record(rampsvc.Entry{
		UserID: 7, Provider: models.ProviderOzav,
		Operation: rampsvc.OpSubmitApplication,
		Err:       ramp.ErrTosRequired,
	})

	require.Equal(t, models.ProviderCallFailed, got.Outcome)
	require.Equal(t, "tos_required", *got.ErrorCode)
}

// The provider's own id for the call travels through an accessor rather than a
// type switch, so a second provider needs no change here. It is what their
// support asks for first, and hunting two of them down on 02/09 meant reading
// logs that were gone the next day.
func TestRecordCarriesTheProviderRequestID(t *testing.T) {
	timeline, repo := newTimeline(t)

	var got *models.ProviderCall
	repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(c *models.ProviderCall) error {
		got = c
		return nil
	}).Once()

	upstream := &ozav.UpstreamError{
		Method: "POST", Path: "/customers",
		Code: "INTERNAL_ERROR", RequestID: "req_abc123",
	}
	timeline.Record(rampsvc.Entry{
		UserID: 7, Provider: models.ProviderOzav,
		Operation: rampsvc.OpSubmitApplication, Err: upstream,
	})

	require.NotNil(t, got.RequestID)
	require.Equal(t, "req_abc123", *got.RequestID)
}

// An audit line that can refuse a submission is a new way for a KYC to fail.
// A timeline with a hole is the better trade, and the hole is visible — the
// reader is already looking for gaps.
func TestRecordNeverFailsTheOperationItDescribes(t *testing.T) {
	timeline, repo := newTimeline(t)
	repo.EXPECT().Create(testifymock.Anything).Return(errors.New("db is down")).Once()

	require.NotPanics(t, func() {
		timeline.Record(rampsvc.Entry{
			UserID: 7, Provider: models.ProviderOzav,
			Operation: rampsvc.OpSubmitApplication,
		})
	})
}

// A provider's final refusal of the applicant is an answer, not an outage:
// Ozav's 409 CUSTOMER_EMAIL_ALREADY_EXISTS reached the timeline as
// provider_unavailable (30/09, user 17), the code for a provider that could
// not be reached. It is recorded under kyc_rejected, the code the client
// contract gives a provider done with the user, with its request id kept.
func TestRecordFilesAFinalRefusalAsTheRefusalItIs(t *testing.T) {
	timeline, repo := newTimeline(t)

	var got *models.ProviderCall
	repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(c *models.ProviderCall) error {
		got = c
		return nil
	}).Once()

	refusal := &ramp.KycRefusal{
		Categories: []string{models.KycRejectionNotEligible},
		Details:    []string{"CUSTOMER_EMAIL_ALREADY_EXISTS"},
		Err: &ozav.UpstreamError{
			Method: "POST", Path: "/customers", Code: "CUSTOMER_EMAIL_ALREADY_EXISTS", RequestID: "req_17",
		},
	}
	timeline.Record(rampsvc.Entry{
		UserID: 17, Provider: models.ProviderOzav, Operation: rampsvc.OpSubmitApplication, Err: refusal,
	})

	require.Equal(t, models.ProviderCallFailed, got.Outcome)
	require.NotNil(t, got.ErrorCode)
	require.Equal(t, "kyc_rejected", *got.ErrorCode)
	require.NotNil(t, got.RequestID)
	require.Equal(t, "req_17", *got.RequestID)
}
