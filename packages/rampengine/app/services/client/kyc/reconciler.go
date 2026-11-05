package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// RefreshFunc reads one customer's KYC from its provider and keeps the answer:
// ProviderKycService.Refresh, the same read the app's "Atualizar status" asks
// for.
type RefreshFunc func(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderKyc, error)

// reconcileStatuses are the postures a provider holds open: it is reviewing,
// or waiting on something the user sends. Approved, rejected and not-started
// rows are settled or the user's to move, and a submission in flight is the
// worker's.
var reconcileStatuses = []string{
	models.KycStatusIncomplete, models.KycStatusUnderReview, models.KycStatusRequiresInfo,
}

// ReconcileReport counts one sweep. Updated is the rows whose posture moved.
type ReconcileReport struct {
	Scanned int
	Updated int
	Skipped int
	Failed  int
}

// Reconciler re-reads the open KYC rows no event has refreshed for a while.
//
// A provider does not always say when it decides: Ozav sends no event when it
// re-reviews a file that was sent again (measured on 23/10/2026), so without
// this the row stays as the last event left it until the user taps refresh.
// It is idempotent: a read that changes nothing writes nothing.
type Reconciler struct {
	repo    repositories.CustomerProviderKycRepository
	refresh RefreshFunc
}

func NewReconciler(repo repositories.CustomerProviderKycRepository, refresh RefreshFunc) *Reconciler {
	return &Reconciler{repo: repo, refresh: refresh}
}

// Sweep refreshes up to limit open rows not read for at least staleAfter,
// oldest first. One customer's failure is counted and does not stop the rest;
// a context that ends does, with what was done reported.
func (r *Reconciler) Sweep(ctx context.Context, staleAfter time.Duration, limit int) (*ReconcileReport, error) {
	rows, err := r.repo.ListStale(reconcileStatuses, time.Now().Add(-staleAfter), limit)
	if err != nil {
		return nil, fmt.Errorf("kyc reconcile: %w", err)
	}
	report := &ReconcileReport{}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("kyc reconcile: %w", err)
		}
		report.Scanned++
		before := rows[i].Posture()
		after, err := r.refresh(ctx, rows[i].CustomerID, rows[i].Provider)
		switch {
		case errors.Is(err, ramp.ErrIntegrationRequired), errors.Is(err, ramp.ErrProviderNotImplemented):
			report.Skipped++
		case err != nil:
			report.Failed++
			facades.Log().WithContext(ctx).Warningf("kyc reconcile: customer %d at %s: %v",
				rows[i].CustomerID, rows[i].Provider, err)
		case after != nil && after.Posture() != before:
			report.Updated++
		}
	}
	return report, nil
}
