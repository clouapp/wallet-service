package kyc

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// applicationSteps are the wizard steps a first application covers: what a
// provider needs before it can create a customer at all. Supporting documents
// are not among them: a provider asks for those after creating the customer,
// by name, in its requirements — or declares the ones it asks of everyone as
// ApplicationDocuments, which PendingStepsFor adds per provider.
var applicationSteps = []string{stepPersonal, stepProfile, stepAddress, stepDocument, stepSelfie}

// NeedsApplication reports whether a provider owes the user's whole
// application: it holds no customer for them, and nothing is on its way to
// create one.
//
// "No customer" is the test rather than any status, because it is the one
// fact that survives every path to here. A provider enabled after the user's
// first KYC has no row or a not_started one; one whose documents arrived
// without an application sits at `incomplete` naming nothing; one whose
// application was refused keeps the refusal. None of them has anything
// upstream to update, and none of them names a requirement, because nothing
// was ever sent that it could answer.
//
// The statuses excluded are the ones where a customer exists or is being
// created even if the link has not caught up: an application in flight
// (`submitting`), under review, decided, or suspended. Asking for another
// application there would stage a second one, or restart a review.
func NeedsApplication(status string, hasExternalCustomer bool) bool {
	if hasExternalCustomer {
		return false
	}
	switch status {
	case models.KycStatusSubmitting, models.KycStatusUnderReview,
		models.KycStatusApproved, models.KycStatusPaused:
		return false
	}
	return true
}

// PendingStepsFor is every wizard step a provider still wants from the user:
// what it named, plus the whole application when it owes one. Every reader of
// "what does this provider still want" goes through here — the listing, the
// 409's steps, the capability banner, the per-provider requirements — so the
// wizard opened from any of them shows the same steps.
//
// The whole application includes the documents the provider declared it wants
// with one (row.ApplicationDocuments): Ozav asks every individual for a bank
// statement, and asking only after its customer exists would send the user
// through the wizard a second time for it.
//
// An approved provider is not asked for its risk-profile fields again (the
// profile step): Ozav keeps listing them on customers it has approved, nothing
// they unlock is blocked, and re-sending them re-opens a review — decided on
// 24/09/2026. Any other step it names stays, because it can still gate a
// capability the approval did not grant.
//
// A provider that refused the user for good owes nothing at all: no
// resubmission reaches it again, so no step could be for it.
func PendingStepsFor(row *models.CustomerProviderKyc) []string {
	if Terminal(row) {
		return nil
	}
	return namedSteps(row)
}

// namedSteps is PendingStepsFor for a provider not refused for good.
func namedSteps(row *models.CustomerProviderKyc) []string {
	if row.Business {
		return businessNamedSteps(row)
	}
	named := PendingStepsFromMissing(OutstandingFor(row))
	if row.KycStatus == models.KycStatusApproved {
		named = withoutStep(named, stepProfile)
	}
	if !row.NeedsApplication {
		return named
	}
	selected := make(map[string]struct{}, len(named)+len(applicationSteps))
	for _, step := range named {
		selected[step] = struct{}{}
	}
	for _, step := range applicationSteps {
		selected[step] = struct{}{}
	}
	for _, step := range PendingStepsFromMissing(row.ApplicationDocuments) {
		selected[step] = struct{}{}
	}
	return sortedPendingSteps(selected)
}

// Terminal reports whether a provider is done with the user: it never
// receives their data again, owes no step, and counts as a refusal in the
// overview. It is the one classification the overview, the listing's
// `terminal` and every path that sends a provider anything read.
//
// A terminal category decides it. A rejection no adapter classified (a row
// from before the categories, a snapshot nothing could read) is terminal only
// when nothing is left to send it — no step it names, no application owed —
// and keeps its old treatment otherwise. The row must be marked, since the
// application it may owe is part of that answer.
func Terminal(row *models.CustomerProviderKyc) bool {
	if row.RejectionTerminal() {
		return true
	}
	return row.KycStatus == models.KycStatusRejected &&
		len(row.DecodedRejectionCategories()) == 0 &&
		len(namedSteps(row)) == 0
}

// stuckSubmission reports whether a row has said `submitting` for longer than
// any staged payload can live. Nothing can be in flight then: the payload is
// gone, and nothing will ever release the row — the database queue never
// resumes a job it reserved, and a worker that failed to load the row exits
// without touching it. guardInFlight lets a new submission through on the same
// rule, and the overview stops reading such a row as under review.
func stuckSubmission(row *models.CustomerProviderKyc, now time.Time) bool {
	return row.KycStatus == models.KycStatusSubmitting && row.UpdatedAt != nil &&
		now.Sub(row.UpdatedAt.StdTime()) > StagingTTL
}

// effectiveStatus is the row's status, except that a stuck submission reads as
// the `incomplete` a released one would have gone back to.
func effectiveStatus(row *models.CustomerProviderKyc, now time.Time) string {
	if stuckSubmission(row, now) {
		return models.KycStatusIncomplete
	}
	return row.KycStatus
}

// MarkNeedsApplication sets NeedsApplication on each row from the user's
// integrations — one read answers every provider — and ApplicationDocuments
// from each provider's adapter. A nil factory declares no documents.
func MarkNeedsApplication(rows []models.CustomerProviderKyc, links []models.CustomerProviderIntegration, factory *ramp.Factory) {
	holds := externalCustomerHolders(links)
	for i := range rows {
		markApplication(&rows[i], holds[rows[i].Provider], factory)
	}
}

// markApplication sets the two fields a row derives rather than stores. They
// are set together, in one place, so no reader of "what does this provider
// still want" can see one without the other.
//
// A provider that refused the user for good owes no application: an Ozav
// create refused over the tax id holds no customer, and without this it read
// as owing the whole application again, forever.
//
// A row stuck on `submitting` is read as what it would be had the submission
// been released (see effectiveStatus): with no customer, the application that
// never landed is owed again.
//
// An application deferred for a provider that creates its customer with the
// documents (ApplicationDeferredAt) is not owed again either: there is no
// customer yet, but the application was given, and what the row still names —
// the documents the create waits for — is all the provider wants.
func markApplication(row *models.CustomerProviderKyc, hasExternalCustomer bool, factory *ramp.Factory) {
	row.NeedsApplication = NeedsApplication(effectiveStatus(row, time.Now()), hasExternalCustomer) &&
		!row.RejectionTerminal() && row.ApplicationDeferredAt == nil
	row.ApplicationDocuments = applicationDocuments(factory, row.Provider)
}

func applicationDocuments(factory *ramp.Factory, provider string) []string {
	if factory == nil {
		return nil
	}
	adapter, err := factory.Get(provider)
	if err != nil {
		return nil
	}
	return ramp.ApplicationDocumentsOf(adapter)
}

// externalCustomerHolders is the set of providers where the user holds a customer.
func externalCustomerHolders(links []models.CustomerProviderIntegration) map[string]bool {
	holds := make(map[string]bool, len(links))
	for i := range links {
		if hasExternalCustomer(&links[i]) {
			holds[links[i].Provider] = true
		}
	}
	return holds
}

func withoutStep(steps []string, drop string) []string {
	var out []string
	for _, step := range steps {
		if step != drop {
			out = append(out, step)
		}
	}
	return out
}
