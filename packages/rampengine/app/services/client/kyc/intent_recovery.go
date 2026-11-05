package kyc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// The bounds of the recovery sweep.
const (
	// intentRecoveryMaxAttempts is how many claims an intent gets before the
	// sweep quarantines it instead.
	intentRecoveryMaxAttempts = 8
	// intentDispatchRetry is how soon a claim the queue refused is due again.
	intentDispatchRetry = time.Minute
	// IntentCodeRecoveryDispatchFailed is the failure code of a claim the
	// queue refused.
	IntentCodeRecoveryDispatchFailed = "recovery_dispatch_failed"
)

// The material retention of an intent held for an operator, in hours.
const (
	DefaultIntentMaterialRetentionHours = 720
	MinIntentMaterialRetentionHours     = 24
	MaxIntentMaterialRetentionHours     = 8760
)

// ErrIntentCustomerConflict refuses reconciling an intent with a customer
// when the person's link already names another one: an operator decides
// between the two before either is linked.
var ErrIntentCustomerConflict = errors.New("kyc: the link already names another customer")

// IntentMaterialRetention is the retention of a held intent's material for a
// configured number of hours. A value outside a day to a year is taken for a
// typo rather than a policy — an hour would scrub what an operator has not
// read yet, and forever keeps personal data no one needs — and answers the
// default.
func IntentMaterialRetention(hours int) time.Duration {
	if hours < MinIntentMaterialRetentionHours || hours > MaxIntentMaterialRetentionHours {
		hours = DefaultIntentMaterialRetentionHours
	}
	return time.Duration(hours) * time.Hour
}

func orRetention(retention time.Duration) time.Duration {
	if retention <= 0 {
		return IntentMaterialRetention(DefaultIntentMaterialRetentionHours)
	}
	return retention
}

// IntentRecoveryReport is what one sweep did.
type IntentRecoveryReport struct {
	// Claimed is every intent the claim returned, quarantined ones included.
	Claimed int
	// Dispatched were handed back to the worker.
	Dispatched int
	// Quarantined came back from the claim in manual review: their material
	// did not open.
	Quarantined int
	// Abandoned came back failed: out of attempts or of their window with
	// not one create sent. Their rows are let go; nothing is dispatched.
	Abandoned int
	// Failed were refused by the queue and rescheduled.
	Failed int
}

// SweepIntentRecovery hands every intent whose next attempt or lease is due
// back to the worker.
//
// The claim is what counts the attempt and leases the intent for one worker
// run (ClaimRecoveryDue), and it leaves last_attempt_at alone — which is how
// the dispatched run tells the sweeper's lease, its own to use, from another
// run's (leasedToAnotherRun). So nothing writes the intent between the claim
// and the dispatch; a dispatch the queue refuses is the one exception, put
// due again a minute out rather than left to its lease.
//
// The run is dispatched under the intent's own submission id: it finds its
// staging, or, with the staging expired, resumes from the prepared body
// (resumeAtomic).
func (s *ProviderKycService) SweepIntentRecovery(ctx context.Context, limit int) (IntentRecoveryReport, error) {
	var report IntentRecoveryReport
	if s.intents == nil {
		return report, errors.New("kyc: no intent repository")
	}
	now := time.Now()
	claimed, err := s.intents.ClaimRecoveryDue(now, now.Add(atomicLease), limit,
		intentRecoveryMaxAttempts, now.Add(s.intentRetention))
	if err != nil {
		return report, fmt.Errorf("kyc: claim due intents: %w", err)
	}
	report.Claimed = len(claimed)
	for i := range claimed {
		intent, material := &claimed[i].Intent, claimed[i].Material
		if claimed[i].Abandoned {
			report.Abandoned++
			s.letGoNeverSent(intent)
			continue
		}
		if material == nil {
			report.Quarantined++
			continue
		}
		if err := ctx.Err(); err != nil {
			// What is left stays leased; its lease running out makes it due
			// for the next sweep.
			return report, err
		}
		if err := s.dispatcher.Dispatch(material.SubmissionID, intent.CustomerID, intent.Provider); err != nil {
			report.Failed++
			facades.Log().Errorf("kyc: recovery: dispatch intent %s: %v", intent.ExternalID, err)
			if err := s.intents.ScheduleRecovery(intent, time.Now().Add(intentDispatchRetry),
				IntentCodeRecoveryDispatchFailed); err != nil {
				facades.Log().Errorf("kyc: recovery: reschedule intent %s: %v", intent.ExternalID, err)
			}
			continue
		}
		report.Dispatched++
	}
	return report, nil
}

// letGoNeverSent releases the row of an intent the claim failed for never
// sending its create — still `submitting` when the worker died with it — with
// a failure the user can send the documents again from, and drops what is
// staged for it. A row no longer `submitting` keeps what it says (release).
func (s *ProviderKycService) letGoNeverSent(intent *models.ProviderKycSubmissionIntent) {
	facades.Log().Warningf("kyc: recovery: intent %s ran out without a create sent; failed", intent.ExternalID)
	s.staging.Forget(intent.SubmissionID)
	adapter, err := s.factory.Get(intent.Provider)
	if err != nil {
		facades.Log().Errorf("kyc: recovery: intent %s: %v", intent.ExternalID, err)
		return
	}
	row, isNew, err := s.loadRow(intent.CustomerID, intent.Provider, adapter)
	if err != nil {
		facades.Log().Errorf("kyc: recovery: intent %s: load row: %v", intent.ExternalID, err)
		return
	}
	s.release(row, isNew, "", intent.Provider, time.Now(), errIntentNeverSent)
}

// IntentResolution is an operator's verdict on an intent held for review.
type IntentResolution struct {
	ExternalID string
	Outcome    string
	Reason     string
	// CustomerID is the customer the operator found at the provider. A
	// reconciliation requires one, and nothing else takes one; it is linked
	// as the worker would have.
	CustomerID string
}

func (r IntentResolution) validate() error {
	switch {
	case r.ExternalID == "":
		return fmt.Errorf("%w: an intent id is required", repositories.ErrInvalidKycResolution)
	case !repositories.ValidKycResolution(r.Outcome, ""):
		return fmt.Errorf("%w: outcome must be reconciled, rejected or cancelled", repositories.ErrInvalidKycResolution)
	case strings.TrimSpace(r.Reason) == "":
		return fmt.Errorf("%w: a reason is required", repositories.ErrInvalidKycResolution)
	case !repositories.ValidKycResolution(r.Outcome, r.Reason):
		return fmt.Errorf("%w: the reason is longer than %d characters",
			repositories.ErrInvalidKycResolution, repositories.KycIntentReasonMaxLength)
	case r.Outcome == models.KycIntentOutcomeReconciled && strings.TrimSpace(r.CustomerID) == "":
		return fmt.Errorf("%w: reconciled needs the customer id it found (--customer-id)", repositories.ErrInvalidKycResolution)
	case r.CustomerID != "" && r.Outcome != models.KycIntentOutcomeReconciled:
		return fmt.Errorf("%w: a customer id only goes with reconciled", repositories.ErrInvalidKycResolution)
	}
	return nil
}

// ResolveIntent closes an intent in manual review with an operator's verdict.
//
// A reconciliation naming the customer links it first — link and row
// together, as linkAndSave does for the worker, the held application and its
// failure cleared — and only onto a link with no customer or this same one;
// another customer is refused (ErrIntentCustomerConflict). The intent is
// resolved after the link, so a link that failed leaves it held for another
// try, and the provider is asked where the customer stands last.
func (s *ProviderKycService) ResolveIntent(ctx context.Context, r IntentResolution) error {
	if err := r.validate(); err != nil {
		return err
	}
	if s.intents == nil {
		return errors.New("kyc: no intent repository")
	}
	intent, _, err := s.intents.FindByExternalID(r.ExternalID)
	// Unreadable material does not stop a verdict: the state is what decides.
	if err != nil && !errors.Is(err, repositories.ErrKycIntentIntegrity) {
		return err
	}
	if intent == nil {
		return repositories.ErrKycIntentNotFound
	}
	if intent.State != models.KycIntentManualReview {
		return repositories.ErrKycIntentNotResolvable
	}
	if r.CustomerID != "" {
		if err := s.linkResolvedCustomer(r.ExternalID, intent.CustomerID, intent.Provider, r.CustomerID); err != nil {
			return err
		}
	}
	// Resolve checks manual_review again under its own lock. Losing a race
	// here — the link written, the intent changed since — leaves the link
	// (the customer exists) and answers the error: re-running the command is
	// the recovery, and links the same customer again as a no-op.
	if err := s.intents.Resolve(r.ExternalID, r.Outcome, r.Reason, time.Now()); err != nil {
		return err
	}
	if r.CustomerID != "" {
		if _, err := s.Refresh(ctx, intent.CustomerID, intent.Provider); err != nil {
			// The verdict and the link stand; the next refresh or webhook
			// brings the row up to date.
			facades.Log().Warningf("kyc: resolve intent %s: refresh %s: %v", r.ExternalID, intent.Provider, err)
		}
	}
	return nil
}

// linkResolvedCustomer links the customer an operator reconciled an intent
// with, onto the row as it stands.
//
// The link write is the compare-and-set that keeps a concurrent replay from
// putting a second customer over it (repositories.ErrLinkConflict); the read
// before it only answers the common case without a write.
func (s *ProviderKycService) linkResolvedCustomer(externalID string, customerID uint, provider, externalCustomerID string) error {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return err
	}
	err = untilSettled(func() error {
		// Still the operator's: something may have resolved it since it was
		// read, and nothing is linked for an intent no longer held.
		intent, _, err := s.intents.FindByExternalID(externalID)
		if err != nil && !errors.Is(err, repositories.ErrKycIntentIntegrity) {
			return err
		}
		if intent == nil {
			return repositories.ErrKycIntentNotFound
		}
		if intent.State != models.KycIntentManualReview {
			return repositories.ErrKycIntentNotResolvable
		}
		link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
		if err != nil {
			return fmt.Errorf("kyc: lookup integration: %w", err)
		}
		if link == nil {
			return ramp.ErrIntegrationRequired
		}
		if link.ExternalCustomerID != nil && *link.ExternalCustomerID != "" && *link.ExternalCustomerID != externalCustomerID {
			return ErrIntentCustomerConflict
		}
		row, isNew, err := s.loadRow(customerID, provider, adapter)
		if err != nil {
			return err
		}
		row.ApplicationDeferredAt = nil
		row.ClearSubmissionFailure()
		written, err := s.linkAndSave(link, externalCustomerID, row, isNew, row.KycStatus)
		if err != nil {
			return err
		}
		if !written {
			return errRowMoved
		}
		return nil
	})
	if errors.Is(err, repositories.ErrLinkConflict) {
		return ErrIntentCustomerConflict
	}
	return err
}

// ScrubIntentMaterial clears the material of intents past their retention,
// and reports how many.
func (s *ProviderKycService) ScrubIntentMaterial(limit int) (int, error) {
	if s.intents == nil {
		return 0, errors.New("kyc: no intent repository")
	}
	return s.intents.ScrubExpiredMaterial(time.Now(), limit)
}
