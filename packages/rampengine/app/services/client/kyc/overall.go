package kyc

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The overall KYC states the app renders.
const (
	OverallNotStarted     = "not_started"
	OverallInReview       = "in_review"
	OverallActionRequired = "action_required"
	OverallApproved       = "approved"
	OverallRejected       = "rejected"
	OverallUnavailable    = "unavailable"
)

// Overall is the one KYC state the user sees across every eligible provider.
//
// Categories is empty unless the status is action_required or rejected, and
// PendingSteps unless it is not_started or action_required.
type Overall struct {
	Status       string
	Categories   []string
	PendingSteps []string
	// PendingDocuments is the company documents a business's providers still
	// name, as XIP purposes: non-empty only while company_documents is owed
	// and the provider said which (API contract §7.2); a person's never.
	PendingDocuments []string
}

// phase is where one provider stands, for the overall rules.
type phase int

const (
	phaseUntouched phase = iota
	// phaseActionable is a provider that refused with something the user can
	// fix: it waits while another provider is still deciding.
	phaseActionable
	// phaseAsking is a provider that asked the user for something, or whose
	// last submission never reached it: nothing is with the provider, so no
	// other provider's review keeps the user from being told.
	phaseAsking
	phaseTerminal
	phaseWorking
	phaseApproved
)

// ComputeOverall folds the eligible providers into the one state the user
// sees. A user is told of a rejection only when no provider is left in
// progress, and approved anywhere is approved — except a provider that is
// approved on the row but still awaiting a pending PIX endorsement (see
// AwaitingPixEndorsementReview) counts as working until Bridge finishes.
//
// rows are the eligible providers as List returns them (marked, synthesized
// where untouched). inFlight names the providers with a submission still
// queued or on its way. submittedBefore is whether the user ever submitted
// anything, which is what tells a provider owing the application to someone
// who never started from one owing it after a failed attempt.
//
// Each provider is put in a phase, then the first rule that matches wins:
// approved; action required while any provider asks the user for something
// (a provider reviewing does not hide it: both providers are launched, and
// each opens rails the other does not, so one's request is never the other's
// to answer); in review while any provider works; not started while every one
// is untouched; action required while any has something the user can send;
// rejected when every one refused for good.
func ComputeOverall(rows []models.CustomerProviderKyc, inFlight map[string]bool, submittedBefore bool, now time.Time) Overall {
	if len(rows) == 0 {
		return Overall{Status: OverallUnavailable}
	}
	phases := make([]phase, len(rows))
	for i := range rows {
		phases[i] = phaseOf(&rows[i], inFlight[rows[i].Provider], submittedBefore, now)
	}
	switch {
	case anyPhase(phases, phaseApproved):
		return Overall{Status: OverallApproved}
	case anyPhase(phases, phaseAsking):
		return actionRequired(rows, phases, now)
	case anyPhase(phases, phaseWorking):
		return Overall{Status: OverallInReview}
	case everyPhase(phases, phaseUntouched):
		return Overall{Status: OverallNotStarted, PendingSteps: owedSteps(rows, phases, now)}
	case anyPhase(phases, phaseActionable) || anyPhase(phases, phaseUntouched):
		return actionRequired(rows, phases, now)
	}
	return Overall{Status: OverallRejected, Categories: []string{terminalCategory(rows)}}
}

func actionRequired(rows []models.CustomerProviderKyc, phases []phase, now time.Time) Overall {
	return Overall{
		Status:           OverallActionRequired,
		Categories:       corrigibleCategories(rows, phases),
		PendingSteps:     owedSteps(rows, phases, now),
		PendingDocuments: pendingDocuments(rows, phases),
	}
}

// phaseOf places one provider. A submission in flight outranks whatever the
// row says: the provider can read `incomplete`, asking for the very files
// still in the queue, for seconds after the application landed. A row stuck
// on `submitting` past the staging TTL is not in flight at all (see
// stuckSubmission).
//
// Nothing that is not with the provider reads as working: a provider is only
// "with the provider" when it is reviewing, suspended, receiving a submission,
// or holds everything it asked for with nothing failed on the way.
func phaseOf(row *models.CustomerProviderKyc, inFlight, submittedBefore bool, now time.Time) phase {
	switch effectiveStatus(row, now) {
	case models.KycStatusApproved:
		if row.AwaitingPixEndorsementReview() {
			return phaseWorking
		}
		return phaseApproved
	case models.KycStatusSubmitting, models.KycStatusUnderReview, models.KycStatusPaused:
		return phaseWorking
	}
	if inFlight {
		return phaseWorking
	}
	if Terminal(row) {
		return phaseTerminal
	}
	if row.NeedsApplication && !submittedBefore {
		return phaseUntouched
	}
	if len(userSteps(row, now)) > 0 {
		if row.KycStatus == models.KycStatusRejected {
			return phaseActionable
		}
		// An application never sent to this provider is not an ask: a
		// provider launched after the user applied elsewhere waits its turn.
		if row.NeedsApplication && row.LastErrorCode == nil {
			return phaseActionable
		}
		return phaseAsking
	}
	// Nothing is the user's to send, nothing failed, and the provider has not
	// decided: it holds everything it asked for (Ozav pending with nothing
	// missing).
	return phaseWorking
}

// userSteps is what one provider is waiting on the user for: a rejected
// row's rejectedSteps; what it names, the whole application where one is
// owed; and, when it names nothing but the last submission never reached it
// (failed at the worker, or stuck past the staging TTL), the documents that
// were on their way.
func userSteps(row *models.CustomerProviderKyc, now time.Time) []string {
	if row.KycStatus == models.KycStatusRejected {
		return rejectedSteps(row)
	}
	if steps := PendingStepsFor(row); len(steps) > 0 {
		return steps
	}
	if row.LastErrorCode != nil || stuckSubmission(row, now) {
		return resendSteps(row)
	}
	return nil
}

// rejectedSteps is what a rejected provider asks for: the steps its categories
// reopen — a person's or a company's — or, for a row no adapter classified,
// what it still names.
func rejectedSteps(row *models.CustomerProviderKyc) []string {
	if categories := row.DecodedRejectionCategories(); len(categories) > 0 {
		return categorySteps(customerTypeOf(row), categories)
	}
	return PendingStepsFor(row)
}

// customerTypeOf is the type of the customer the row is of.
func customerTypeOf(row *models.CustomerProviderKyc) string {
	if row.Business {
		return models.CustomerTypeBusiness
	}
	return models.CustomerTypeIndividual
}

// resendSteps is the document pass a provider did not receive: the identity
// document and the selfie, and the documents it asks of every customer — for
// a business, its people's IDs and its own documents. The row cannot say
// which files it was still missing — Ozav's still_required is only read on a
// sync — so the whole pass is asked again.
func resendSteps(row *models.CustomerProviderKyc) []string {
	if row.Business {
		return []string{models.KybStepOwners, models.KybStepCompanyDocuments}
	}
	selected := map[string]struct{}{stepDocument: {}, stepSelfie: {}}
	for _, step := range PendingStepsFromMissing(row.ApplicationDocuments) {
		selected[step] = struct{}{}
	}
	return sortedPendingSteps(selected)
}

// owedSteps is the union of what the actionable and untouched providers want,
// in wizard order (userSteps for each).
func owedSteps(rows []models.CustomerProviderKyc, phases []phase, now time.Time) []string {
	selected := map[string]struct{}{}
	for i := range rows {
		if phases[i] != phaseActionable && phases[i] != phaseAsking && phases[i] != phaseUntouched {
			continue
		}
		for _, step := range userSteps(&rows[i], now) {
			selected[step] = struct{}{}
		}
	}
	return sortedPendingSteps(selected)
}

// corrigibleCategories is the union of the actionable providers' categories,
// in the order the categories are listed.
func corrigibleCategories(rows []models.CustomerProviderKyc, phases []phase) []string {
	selected := map[string]bool{}
	for i := range rows {
		if phases[i] != phaseActionable && phases[i] != phaseAsking {
			continue
		}
		for _, category := range rows[i].DecodedRejectionCategories() {
			selected[category] = true
		}
	}
	var out []string
	for _, category := range models.KycRejectionCategories {
		if selected[category] {
			out = append(out, category)
		}
	}
	return out
}

// terminalCategory is what an all-terminal user is told: the tax id when any
// provider says so, since that one has a way out (the original account), and
// not_eligible otherwise.
func terminalCategory(rows []models.CustomerProviderKyc) string {
	for i := range rows {
		for _, category := range rows[i].DecodedRejectionCategories() {
			if category == models.KycRejectionTaxIDInUse {
				return models.KycRejectionTaxIDInUse
			}
		}
	}
	return models.KycRejectionNotEligible
}

// categorySteps is the union of the steps the categories reopen for a
// customer of customerType.
func categorySteps(customerType string, categories []string) []string {
	var out []string
	for _, category := range categories {
		out = append(out, models.KycRejectionStepsFor(customerType, category)...)
	}
	return out
}

// pendingDocuments is the union of the company documents the actionable
// providers of a business name, in the order the app asks them; empty for a
// person, whose providers name none.
func pendingDocuments(rows []models.CustomerProviderKyc, phases []phase) []string {
	var missing []string
	for i := range rows {
		if (phases[i] == phaseActionable || phases[i] == phaseAsking) && rows[i].Business {
			missing = append(missing, OutstandingFor(&rows[i])...)
		}
	}
	return PendingDocumentsFromMissing(missing)
}

func anyPhase(phases []phase, want phase) bool {
	for _, p := range phases {
		if p == want {
			return true
		}
	}
	return false
}

func everyPhase(phases []phase, want phase) bool {
	for _, p := range phases {
		if p != want {
			return false
		}
	}
	return true
}

// OverviewView is the overall state and the per-provider list it was read from
// — the same list List answers.
type OverviewView struct {
	Overall   Overall
	CheckedAt time.Time
	Providers []models.CustomerProviderKyc
}
