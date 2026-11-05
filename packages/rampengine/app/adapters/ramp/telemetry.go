package ramp

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Telemetry lives beside the router rather than in the ramp services because
// the router is its busiest caller and a service-side package would be an
// import cycle.
//
// Telemetry is the one place the ramp stack reports what it did.
//
// A multi-provider router with no telemetry is a black box exactly where the
// money is: when an order fails, the questions are which provider was tried,
// why the others were skipped, and how long the call took — and none of those
// are answerable from a stack trace after the fact.
//
// Everything here is structured log fields rather than a metrics client. The
// deployment has no metrics backend yet, and log fields with stable names are
// what a backend would scrape anyway; adding one later is a collector change,
// not a code change.
//
// The rule that governs every function below: field names, never field values,
// for anything that touched a person. No request body, no response body, no
// customer id, no PIX key, no amount tied to an identity.

// Outcome classifies how a router resolution or a provider call ended.
type Outcome string

const (
	OutcomeOK                  Outcome = "ok"
	OutcomeNoProvider          Outcome = "no_provider_available"
	OutcomeIntegrationRequired Outcome = "integration_required"
	OutcomeKycPending          Outcome = "kyc_pending"
	OutcomeKycRejected         Outcome = "kyc_rejected"
	OutcomeRailNotEligible     Outcome = "rail_not_eligible"
	OutcomeUpstreamError       Outcome = "upstream_error"
)

// LogResolve records which provider a rail resolved to, or why none did.
//
// The rejection reason is the useful half: "no provider available" and "the
// user has not finished KYC" look identical from the outside and need
// completely different responses.
func LogResolve(rail, provider string, outcome Outcome, candidates int) {
	facades.Log().With(map[string]any{
		"event":      "ramp.resolve",
		"rail":       rail,
		"provider":   provider,
		"outcome":    string(outcome),
		"candidates": candidates,
	}).Info("ramp: provider resolution")
}

// LogUpstreamCall records one provider call.
//
// Latency is here because a provider degrading is visible in it long before it
// shows up as an error, and the create path cannot retry elsewhere once the
// request has gone out — so slow is the warning that matters.
func LogUpstreamCall(provider, operation string, started time.Time, err error) {
	fields := map[string]any{
		"event":      "ramp.upstream",
		"provider":   provider,
		"operation":  operation,
		"latency_ms": time.Since(started).Milliseconds(),
		"outcome":    string(OutcomeOK),
	}
	if err != nil {
		fields["outcome"] = string(OutcomeUpstreamError)
		// The error string, not the response body. Adapter errors are built to
		// carry a machine code and nothing the provider echoed back.
		fields["error"] = err.Error()
		facades.Log().With(fields).Warning("ramp: upstream call failed")
		return
	}
	facades.Log().With(fields).Info("ramp: upstream call")
}

// LogFailover records that the router moved to the next provider.
//
// It should be rare. A rise in this counter means the head of a rail order is
// unhealthy, and it is the signal to reorder the rail before users notice.
func LogFailover(rail, from, to, reason string) {
	facades.Log().With(map[string]any{
		"event":    "ramp.failover",
		"rail":     rail,
		"from":     from,
		"to":       to,
		"reason":   reason,
		"severity": "attention",
	}).Warning("ramp: failed over to the next provider")
}

// LogWebhookLag records how long a delivery took to reach us.
//
// Lag is what distinguishes "the provider is slow" from "our queue is backed
// up", and the two have different owners.
func LogWebhookLag(provider, eventType string, occurredAt *time.Time) {
	fields := map[string]any{
		"event":      "ramp.webhook",
		"provider":   provider,
		"event_type": eventType,
	}
	if occurredAt != nil {
		fields["lag_ms"] = time.Since(*occurredAt).Milliseconds()
	}
	facades.Log().With(fields).Info("ramp: webhook received")
}

// LogReconcile records a sweep. A non-zero `updated` is the number of orders
// the event stream failed to deliver, which is the health of the webhook path
// measured from the other side.
func LogReconcile(scanned, updated, expired, failed int) {
	level := facades.Log().With(map[string]any{
		"event":   "ramp.reconcile",
		"scanned": scanned,
		"updated": updated,
		"expired": expired,
		"failed":  failed,
	})
	if updated > 0 || failed > 0 {
		level.Warning("ramp: reconciliation corrected orders the event stream missed")
		return
	}
	level.Info("ramp: reconciliation found nothing to correct")
}

// LogVirtualAccountSync records a standing-account sweep. `fee_pushed` is the
// answer to the only question an operator asks after editing a rail's fee: did
// it reach the accounts already provisioned, which keep charging what they were
// created with until it does.
func LogVirtualAccountSync(scanned, updated, feePushed, failed int) {
	level := facades.Log().With(map[string]any{
		"event":      "ramp.account_sync",
		"scanned":    scanned,
		"updated":    updated,
		"fee_pushed": feePushed,
		"failed":     failed,
	})
	if updated > 0 || failed > 0 {
		level.Warning("ramp: standing accounts drifted from the provider or the rail")
		return
	}
	level.Info("ramp: standing accounts match the provider and the rail")
}

// LogKycSubmission records the outcome of a queued submission. The provider
// and the outcome only — the payload is a person's identity documents.
func LogKycSubmission(provider, outcome string, started time.Time) {
	facades.Log().With(map[string]any{
		"event":      "ramp.kyc_submit",
		"provider":   provider,
		"outcome":    outcome,
		"latency_ms": time.Since(started).Milliseconds(),
	}).Info("ramp: kyc submission finished")
}

// LogUnmappedRejection records a provider refusal an adapter could not
// translate onto the rejection categories, which it answered as not_eligible.
// It is a warning because every one of them is a mapping to write: until then
// the user is told to contact support for a reason that may be theirs to fix.
//
// codes carries the provider's codes and generic labels, never free text a
// provider wrote about the customer.
// LogUnnamedKycRequest warns that a provider is waiting on the integrator
// while naming nothing to send: no step can answer it, so an operator has to
// take it to the provider. The external customer id is the provider's opaque
// handle, not personal data.
func LogUnnamedKycRequest(provider, externalCustomerID, reasonCode string) {
	facades.Log().With(map[string]any{
		"event":                "ramp.kyc_request_unnamed",
		"provider":             provider,
		"external_customer_id": externalCustomerID,
		"reason_code":          reasonCode,
	}).Warning("ramp: provider awaits the integrator but names nothing to send")
}

func LogUnmappedRejection(provider string, codes []string) {
	if len(codes) == 0 {
		return
	}
	facades.Log().With(map[string]any{
		"event":    "ramp.kyc_rejection_unmapped",
		"provider": provider,
		"codes":    codes,
	}).Warning("ramp: kyc rejection not mapped, answered as not_eligible")
}
