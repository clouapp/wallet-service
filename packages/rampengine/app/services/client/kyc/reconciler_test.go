package kyc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func staleRow(customerID uint, provider, status string, requirements ...string) models.CustomerProviderKyc {
	row := overallRow(provider, status)
	row.CustomerID = customerID
	row.SetRequirements(requirements)
	return row
}

// reconcilerWith stubs the listing and returns the reconciler reading it.
func reconcilerWith(t *testing.T, rows []models.CustomerProviderKyc, refresh kyc.RefreshFunc) (*kyc.Reconciler, *mocks.MockCustomerProviderKycRepository) {
	t.Helper()
	repo := mocks.NewMockCustomerProviderKycRepository(t)
	repo.EXPECT().ListStale(mock.Anything, mock.Anything, mock.Anything).Return(rows, nil)
	return kyc.NewReconciler(repo, refresh), repo
}

// What the sweep asks the repository for is the contract: only the rows a
// provider still holds open, not read for at least staleAfter, capped.
func TestReconcilerSweepAsksForTheOpenRowsNotReadLately(t *testing.T) {
	repo := mocks.NewMockCustomerProviderKycRepository(t)
	var gotStatuses []string
	var gotBefore time.Time
	var gotLimit int
	repo.EXPECT().ListStale(mock.Anything, mock.Anything, mock.Anything).
		Run(func(statuses []string, before time.Time, limit int) {
			gotStatuses, gotBefore, gotLimit = statuses, before, limit
		}).Return(nil, nil)

	started := time.Now()
	_, err := kyc.NewReconciler(repo, nil).Sweep(context.Background(), 30*time.Minute, 50)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		models.KycStatusIncomplete, models.KycStatusUnderReview, models.KycStatusRequiresInfo,
	}, gotStatuses)
	require.WithinDuration(t, started.Add(-30*time.Minute), gotBefore, 5*time.Second)
	require.Equal(t, 50, gotLimit)
}

func TestReconcilerSweepCountsOnlyWhatChanged(t *testing.T) {
	rows := []models.CustomerProviderKyc{
		staleRow(1, models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
		staleRow(2, models.ProviderOzav, models.KycStatusUnderReview),
	}
	refreshed := map[uint]string{}
	reconciler, _ := reconcilerWith(t, rows, func(_ context.Context, customerID uint, provider string) (*models.CustomerProviderKyc, error) {
		refreshed[customerID] = provider
		if customerID == 1 {
			// Ozav took the new file: the row moved.
			return &models.CustomerProviderKyc{CustomerID: 1, Provider: provider, KycStatus: models.KycStatusUnderReview,
				Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RejectionCategories: "[]"}, nil
		}
		same := rows[1]
		return &same, nil
	})

	report, err := reconciler.Sweep(context.Background(), time.Minute, 10)

	require.NoError(t, err)
	require.Equal(t, map[uint]string{1: models.ProviderOzav, 2: models.ProviderOzav}, refreshed)
	require.Equal(t, &kyc.ReconcileReport{Scanned: 2, Updated: 1}, report)
}

func TestReconcilerSweepKeepsGoingPastAProviderThatFails(t *testing.T) {
	rows := []models.CustomerProviderKyc{
		staleRow(1, models.ProviderBridge, models.KycStatusUnderReview),
		staleRow(2, models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
	}
	var order []uint
	reconciler, _ := reconcilerWith(t, rows, func(_ context.Context, customerID uint, _ string) (*models.CustomerProviderKyc, error) {
		order = append(order, customerID)
		if customerID == 1 {
			return nil, fmt.Errorf("kyc: refresh at bridge: %w", ramp.ErrProviderFailed)
		}
		row := rows[1]
		row.KycStatus = models.KycStatusUnderReview
		return &row, nil
	})

	report, err := reconciler.Sweep(context.Background(), time.Minute, 10)

	require.NoError(t, err)
	require.Equal(t, []uint{1, 2}, order, "the failure of one customer does not hold back the next")
	require.Equal(t, &kyc.ReconcileReport{Scanned: 2, Updated: 1, Failed: 1}, report)
}

// A row whose customer was never linked at the provider, or whose provider
// has no adapter, has nothing to read: it is not an outage.
func TestReconcilerSweepSkipsWhatThereIsNothingToReadFor(t *testing.T) {
	rows := []models.CustomerProviderKyc{
		staleRow(1, models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
		staleRow(2, models.ProviderMutualPay, models.KycStatusUnderReview),
	}
	reconciler, _ := reconcilerWith(t, rows, func(_ context.Context, customerID uint, provider string) (*models.CustomerProviderKyc, error) {
		if customerID == 1 {
			return nil, ramp.ErrIntegrationRequired
		}
		return nil, fmt.Errorf("provider %q: %w", provider, ramp.ErrProviderNotImplemented)
	})

	report, err := reconciler.Sweep(context.Background(), time.Minute, 10)

	require.NoError(t, err)
	require.Equal(t, &kyc.ReconcileReport{Scanned: 2, Skipped: 2}, report)
}

// The run is bounded by its context: one that is over stops the sweep and says
// what it did get through.
func TestReconcilerSweepStopsWhenItsContextEnds(t *testing.T) {
	rows := []models.CustomerProviderKyc{
		staleRow(1, models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
		staleRow(2, models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	reconciler, _ := reconcilerWith(t, rows, func(_ context.Context, _ uint, _ string) (*models.CustomerProviderKyc, error) {
		calls++
		cancel()
		row := rows[0]
		return &row, nil
	})

	report, err := reconciler.Sweep(ctx, time.Minute, 10)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, report.Scanned)
}

func TestReconcilerSweepReportsAListingThatFails(t *testing.T) {
	repo := mocks.NewMockCustomerProviderKycRepository(t)
	repo.EXPECT().ListStale(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("db down"))

	_, err := kyc.NewReconciler(repo, nil).Sweep(context.Background(), time.Minute, 10)

	require.ErrorContains(t, err, "db down")
}
