package kyc_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// expectSubmitRequest stubs everything a submission reads before it stages.
func (h *harness) expectSubmitRequest(status string) {
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).
		Return(linkedIntegration(""), nil).Once()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(kycRow(status), nil).Once()
}

// Identity documents must not linger in Redis for a submission nobody will send,
// and a row left on `submitting` would refuse every retry.
func TestSubmitApplicationLeavesNothingInFlightWhenStagingFails(t *testing.T) {
	down := errors.New("redis: connection refused")
	cases := map[string]struct {
		putErr, markErr error
		forgotten       bool
	}{
		"payload not staged": {putErr: down},
		"row not marked":     {markErr: down, forgotten: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.expectSubmitRequest(models.KycStatusNotStarted)
			h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", c.putErr).Once()
			if c.putErr == nil {
				h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(c.markErr == nil, c.markErr).Once()
			}
			if c.forgotten {
				h.staging.EXPECT().Forget("stg_1").Once()
			}

			_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
			require.ErrorIs(t, err, down)
			h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
	}
}

// A submission the queue refused is not in flight: the payload goes, and the row
// returns to where it was so the user can submit again.
func TestSubmitApplicationUndoesTheStagingWhenTheQueueRefuses(t *testing.T) {
	queueDown := errors.New("redis: connection refused")
	for name, restoreErr := range map[string]error{"row restored": nil, "restore fails": errors.New("pq: reset")} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.expectSubmitRequest(models.KycStatusNotStarted)
			h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
			var written []string
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(row *models.CustomerProviderKyc, _ string) (bool, error) {
					written = append(written, row.KycStatus)
					if len(written) == 2 {
						return restoreErr == nil, restoreErr
					}
					return true, nil
				}).Twice()
			h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(queueDown).Once()
			h.staging.EXPECT().Forget("stg_1").Once()

			_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
			require.ErrorIs(t, err, queueDown)
			require.Equal(t, []string{models.KycStatusSubmitting, models.KycStatusNotStarted}, written)
		})
	}
}

// The submission was accepted either way, so the 202 answers the staged row
// rather than a 500 over a read that only refreshes it.
func TestSubmitApplicationAnswersTheStagedRowWhenTheReloadMisses(t *testing.T) {
	for name, reloadErr := range map[string]error{"row gone": nil, "reload fails": errors.New("pq: reset")} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.expectSubmitRequest(models.KycStatusNotStarted)
			h.staging.EXPECT().Put(testifymock.Anything).Return("stg_1", nil).Once()
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
			h.dispatcher.EXPECT().Dispatch("stg_1", uint(7), models.ProviderBridge).Return(nil).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, reloadErr).Once()

			row, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
			require.NoError(t, err)
			require.Equal(t, models.KycStatusSubmitting, row.KycStatus)
		})
	}
}

// A double tap stages one payload: the lock makes the in-flight check and the
// mark one step. The negative control proves the test sees the race it guards.
func TestConcurrentSubmissionsStageOnePayload(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized by the lock":              {serialize: true, want: 1},
		"negative control: a lock that isn't": {serialize: false, want: 2},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pair := race.New()
			h := newHarnessWith(t, pair.Locker(t, c.serialize), "database")
			h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(linkedIntegration(""), nil)

			var mu sync.Mutex
			stored := *kycRow(models.KycStatusNotStarted)
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
				RunAndReturn(func(uint, string) (*models.CustomerProviderKyc, error) {
					mu.Lock()
					defer mu.Unlock()
					row := stored
					return &row, nil
				})
			h.kycRepo.EXPECT().UpdateIfStatus(testifymock.Anything, testifymock.Anything).
				RunAndReturn(func(row *models.CustomerProviderKyc, status string) (bool, error) {
					pair.Write()
					mu.Lock()
					defer mu.Unlock()
					if stored.KycStatus != status {
						return false, nil
					}
					stored = *row
					return true, nil
				})
			// Without the lock the second mark misses and lets its payload go.
			h.staging.EXPECT().Forget(testifymock.Anything).Maybe()
			var staged atomic.Int32
			h.staging.EXPECT().Put(testifymock.Anything).RunAndReturn(func(kyc.StagedSubmission) (string, error) {
				return fmt.Sprintf("stg_%d", staged.Add(1)), nil
			})
			h.dispatcher.EXPECT().Dispatch(testifymock.Anything, uint(7), models.ProviderBridge).Return(nil)

			errs := pair.Run(2, func() error {
				_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, fullApplication())
				return err
			})
			for _, err := range errs {
				if err != nil {
					require.ErrorIs(t, err, kyc.ErrSubmissionInFlight)
				}
			}
			require.Equal(t, c.want, staged.Load())
		})
	}
}
