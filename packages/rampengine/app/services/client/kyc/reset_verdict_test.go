package kyc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// refusedRow is a row a provider refused for good, carrying everything a
// verdict leaves behind and the terms the user already accepted.
func refusedRow() *models.CustomerProviderKyc {
	row := kycRow(models.KycStatusRejected)
	row.SetRejectionCategories([]string{models.KycRejectionTaxIDInUse})
	row.SetRejectionDetails([]string{"CUSTOMER_ALREADY_EXISTS"})
	row.SetRejectionReasons([]string{"duplicate tax id"})
	row.SetRequirements([]string{"proof_of_address"})
	row.Endorsements = `[{"name":"pix","status":"revoked"}]`
	row.RawStatus = `{"status":"rejected"}`
	row.CollectionMode = models.KycModeHostedFallback
	linkID, url := "kyc_link_1", "https://bridge.xyz/kyc/abc"
	row.HostedKycLinkID, row.HostedKycURL = &linkID, &url
	row.RecordSubmissionFailure("provider_rejected", "CUSTOMER_ALREADY_EXISTS")
	synced := time.Now().Add(-time.Hour)
	row.LastSyncedAt = &synced

	agreement, session := "sa_1", "tos_session_1"
	expires, accepted := time.Now().Add(time.Hour), time.Now().Add(-2*time.Hour)
	row.TosStatus = models.TosStatusApproved
	row.SignedAgreementID, row.TosSessionID = &agreement, &session
	row.TosExpiresAt, row.TosAcceptedAt = &expires, &accepted
	return row
}

// The verdict goes, and nothing the user did to reach it does: the terms are
// kept, since Bridge never echoes the agreement id back and losing it loops the
// user on the terms step. The jsonb lists are written empty, never NULL — the
// columns refuse it.
func TestResetVerdictClearsTheVerdictAndKeepsTheTerms(t *testing.T) {
	h := newHarness(t)
	stored := refusedRow()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(stored, nil).Once()
	var written models.CustomerProviderKyc
	var entry *models.AdminAuditLog
	h.kycRepo.EXPECT().UpdateRecorded(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, recorded *models.AdminAuditLog) error {
			written, entry = *row, recorded
			return nil
		}).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil)
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()

	got, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))

	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, got.PreviousStatus)
	require.Equal(t, []string{models.KycRejectionTaxIDInUse}, got.PreviousCategories)

	require.Equal(t, models.KycStatusNotStarted, written.KycStatus)
	for name, column := range map[string]string{
		"endorsements": written.Endorsements, "requirements": written.Requirements,
		"rejection_reasons": written.RejectionReasons, "rejection_categories": written.RejectionCategories,
		"rejection_details": written.RejectionDetails,
	} {
		require.Equal(t, "[]", column, name)
	}
	require.Equal(t, "{}", written.RawStatus)
	require.Equal(t, models.KycModeAPIDocs, written.CollectionMode, "the hosted fallback goes with the verdict")
	require.Nil(t, written.HostedKycLinkID)
	require.Nil(t, written.HostedKycURL)
	require.Nil(t, written.LastErrorCode)
	require.Nil(t, written.LastErrorDetail)
	require.Nil(t, written.LastErrorAt)
	require.Nil(t, written.LastSyncedAt)

	require.Equal(t, models.TosStatusApproved, written.TosStatus)
	require.Equal(t, "sa_1", *written.SignedAgreementID)
	require.Equal(t, "tos_session_1", *written.TosSessionID)
	require.NotNil(t, written.TosExpiresAt)
	require.NotNil(t, written.TosAcceptedAt)
	require.Equal(t, uint(42), written.ID, "rewritten in place, never soft-deleted")
	require.Equal(t, "kyc.reset", entry.Action, "the row and its entry are written together")
	require.Equal(t, `{"previous_status":"rejected"}`, entry.Metadata, "the entry is built from the row as it was")

	require.Equal(t, models.KycStatusNotStarted, got.Row.KycStatus)
	require.True(t, got.Row.NeedsApplication, "no customer: the next application creates or adopts one")
}

// With a customer linked the row is read back from the provider, so it shows
// the provider's current truth rather than a blank XIP made up.
func TestResetVerdictRefreshesALinkedCustomer(t *testing.T) {
	h := newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refusedRow(), nil).Once()
	h.kycRepo.EXPECT().UpdateRecorded(testifymock.Anything, testifymock.Anything).Return(nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration("cus_1"), nil)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}).
		Return(&ramp.KycState{Status: models.KycStatusIncomplete, Requirements: []string{"selfie"}}, nil).Once()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()

	got, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))

	require.NoError(t, err)
	require.Equal(t, models.KycStatusIncomplete, got.Row.KycStatus)
	require.Equal(t, models.KycStatusRejected, got.PreviousStatus)
}

// The reset is committed before the provider is asked. A provider that fails
// then leaves the reset standing, and the operator refreshes later.
func TestResetVerdictKeepsTheResetWhenTheRefreshFails(t *testing.T) {
	h := newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refusedRow(), nil).Once()
	h.kycRepo.EXPECT().UpdateRecorded(testifymock.Anything, testifymock.Anything).Return(nil).Once()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration("cus_1"), nil)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(kycRow(models.KycStatusNotStarted), nil).Once()
	h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).
		Return(nil, ramp.ErrProviderFailed).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()

	got, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))

	require.NoError(t, err)
	require.Equal(t, models.KycStatusNotStarted, got.Row.KycStatus)
}

// Nothing to reset is a miss, and one on its way to the provider is refused:
// the worker would write its answer over the reset.
func TestResetVerdictRefusesAMissingOrInFlightRow(t *testing.T) {
	fresh := carbon.NewDateTime(carbon.Now())
	inFlight := kycRow(models.KycStatusSubmitting)
	inFlight.UpdatedAt = fresh
	for name, tc := range map[string]struct {
		row  *models.CustomerProviderKyc
		want error
	}{
		"no row":    {nil, kyc.ErrKycNotFound},
		"in flight": {inFlight, kyc.ErrSubmissionInFlight},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(tc.row, nil).Once()

			_, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))

			require.ErrorIs(t, err, tc.want)
			h.kycRepo.AssertNotCalled(t, "UpdateRecorded", testifymock.Anything, testifymock.Anything)
		})
	}
}

func TestResetVerdictRefusesAnUnknownProvider(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.ResetVerdict(context.Background(), 7, "paypal", recordedAs("kyc.reset"))

	require.Error(t, err)
	h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
}

// A reset or an unlink and a submission for the same row take the same lock,
// so neither writes over a row the other read: a reset never turns a staged
// submission's `submitting` back into not_started. The fake repository counts
// a write whose read is stale; the negative control proves the test sees the
// race. ForgetVerdict's persistence writes through the same fake store.
func TestClearingAVerdictAndASubmissionNeverWriteOverEachOther(t *testing.T) {
	clearers := map[string]func(h *harness) error{
		"reset": func(h *harness) error {
			_, err := h.svc.ResetVerdict(context.Background(), 7, models.ProviderBridge, recordedAs("kyc.reset"))
			return err
		},
		"forget": func(h *harness) error {
			return h.svc.ForgetVerdict(context.Background(), 7, models.ProviderBridge, func(row *models.CustomerProviderKyc) error {
				return h.kycRepo.Update(row)
			})
		},
	}
	cases := map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized by the submit lock":       {serialize: true, want: 0},
		"negative control: a lock that isn't": {serialize: false, want: 1},
	}
	for clearer, clear := range clearers {
		for name, c := range cases {
			t.Run(clearer+"/"+name, func(t *testing.T) {
				pair := race.New()
				h := newHarnessWith(t, pair.Locker(t, c.serialize), "database")
				stale := contendedRow(h, pair)

				calls := []func() error{
					func() error {
						_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
						return err
					},
					func() error { return clear(h) },
				}
				var next atomic.Int32
				errs := pair.Run(2, func() error { return calls[next.Add(1)-1]() })
				for _, err := range errs {
					if err != nil {
						require.True(t, errors.Is(err, kyc.ErrSubmissionInFlight), "unexpected: %v", err)
					}
				}
				require.Equal(t, c.want, stale.Load())
			})
		}
	}
}

// contendedRow backs the harness with one corrigible rejected row a
// submission and a clear both read and write, and counts the writes whose
// read another write had already overtaken — a conditional write
// (UpdateIfStatus) counts when it is attempted from such a read, whether or
// not its condition then lets it land: the lock is what keeps every read
// current, and that is what this measures.
func contendedRow(h *harness, pair *race.Race) *atomic.Int32 {
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil)
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil).Maybe()
	h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Maybe()
	h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Maybe()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Maybe()

	var mu sync.Mutex
	stored := *kycRow(models.KycStatusRejected)
	stored.SetRejectionCategories([]string{models.KycRejectionSelfieMismatch})
	version := 0
	readAt := map[*models.CustomerProviderKyc]int{}
	stale := &atomic.Int32{}
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) {
			mu.Lock()
			defer mu.Unlock()
			row := stored
			readAt[&row] = version
			return &row, nil
		})
	h.kycRepo.EXPECT().UpdateRecorded(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, _ *models.AdminAuditLog) error {
			return h.kycRepo.Update(row)
		}).Maybe()
	h.kycRepo.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.CustomerProviderKyc) error {
		pair.Write()
		mu.Lock()
		defer mu.Unlock()
		if readAt[row] != version {
			stale.Add(1)
		}
		version++
		readAt[row] = version
		stored = *row
		return nil
	}).Maybe()
	h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(row *models.CustomerProviderKyc, status string) (bool, error) {
			pair.Write()
			mu.Lock()
			defer mu.Unlock()
			if readAt[row] != version {
				stale.Add(1)
			}
			if stored.KycStatus != status {
				return false, nil
			}
			version++
			readAt[row] = version
			stored = *row
			return true, nil
		}).Maybe()
	h.staging.EXPECT().Forget(testifymock.Anything).Maybe()
	return stale
}

// Forgetting a provider (the admin unlink) clears the row the way a reset does
// and hands it to the caller's persistence under the same lock, which writes
// it — soft-deleted — with the link.
func TestForgetVerdictHandsTheClearedRowToPersist(t *testing.T) {
	h := newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refusedRow(), nil).Once()

	var handed *models.CustomerProviderKyc
	err := h.svc.ForgetVerdict(context.Background(), 7, models.ProviderBridge, func(row *models.CustomerProviderKyc) error {
		handed = row
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, handed)
	require.Equal(t, models.KycStatusNotStarted, handed.KycStatus)
	require.Equal(t, "[]", handed.RejectionCategories)
	require.Equal(t, "sa_1", *handed.SignedAgreementID)
	h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

func TestForgetVerdictHandsNothingWhenThereIsNoRow(t *testing.T) {
	h := newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()

	called := false
	err := h.svc.ForgetVerdict(context.Background(), 7, models.ProviderBridge, func(row *models.CustomerProviderKyc) error {
		called = true
		require.Nil(t, row)
		return nil
	})

	require.NoError(t, err)
	require.True(t, called, "the link still goes without a KYC row")
}

func TestForgetVerdictRefusesInFlightAndReturnsThePersistError(t *testing.T) {
	inFlight := kycRow(models.KycStatusSubmitting)
	inFlight.UpdatedAt = carbon.NewDateTime(carbon.Now())
	h := newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(inFlight, nil).Once()

	err := h.svc.ForgetVerdict(context.Background(), 7, models.ProviderBridge, func(*models.CustomerProviderKyc) error {
		t.Fatal("an in-flight row is not handed over")
		return nil
	})
	require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)

	h = newHarness(t)
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(refusedRow(), nil).Once()
	down := errors.New("pq: connection reset")
	err = h.svc.ForgetVerdict(context.Background(), 7, models.ProviderBridge, func(*models.CustomerProviderKyc) error {
		return down
	})
	require.ErrorIs(t, err, down)
}

// recordedAs builds a reset's entry from the row as it was.
func recordedAs(action string) func(*models.CustomerProviderKyc) *models.AdminAuditLog {
	return func(previous *models.CustomerProviderKyc) *models.AdminAuditLog {
		return &models.AdminAuditLog{Action: action, Metadata: `{"previous_status":"` + previous.KycStatus + `"}`}
	}
}
