// Package ramps exposes the integration, KYC and on/off-ramp endpoints to the
// mobile client.
package ramps

import (
	"errors"
	"fmt"
	"slices"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// errorStatus is the HTTP status that goes with a stable error code.
//
// It is deliberately a second, small table rather than a second switch on the
// sentinels: resources.ErrorCode already resolved which sentinel matched, so
// this only has to know each code's own status, not repeat the matching.
// Several codes share a status because several sentinels above share both —
// see resources.ErrorCode for which.
var errorStatus = map[string]int{
	"integration_required": contractshttp.StatusConflict,
	"kyc_pending":          contractshttp.StatusConflict,
	// The same 409 as kyc_pending, and its own code: the request was well
	// formed and the state it depended on refuses it. What differs is what the
	// app does — kyc_pending opens that provider's wizard, kyc_rejected must
	// not, because nothing the user types changes the decision.
	"kyc_rejected": contractshttp.StatusConflict,
	// 409 like kyc_rejected, with no provider: the tax identity and residence
	// the user declared do not open this rail anywhere in its order, or the
	// provider said it is not available where they live. No wizard changes
	// it; the app offers the rail that is open instead.
	"rail_not_eligible":   contractshttp.StatusConflict,
	"tos_required":        contractshttp.StatusConflict,
	"tos_not_required":    contractshttp.StatusConflict,
	"tos_session_unknown": contractshttp.StatusConflict,
	// 409 alongside the other terms conditions: the request was well formed and
	// the state it depended on is gone, which is what the client acts on by
	// reopening the terms step.
	"tos_session_expired": contractshttp.StatusConflict,
	// 422 instead: this one is a field the request should have carried and did
	// not, and telling the client to reopen the terms step would send it round
	// a loop that fails identically every time.
	"tos_legal_name_required": contractshttp.StatusUnprocessableEntity,
	"mfa_required":            contractshttp.StatusForbidden,
	// 422, not 403: the credential was supplied and read, and what failed is
	// its value. 403 would say the user may not do this at all, which is the
	// opposite of "type it again".
	"mfa_invalid":          contractshttp.StatusUnprocessableEntity,
	"submission_in_flight": contractshttp.StatusConflict,
	// 409 like tos_required: the documents are well formed, and what they
	// lack is the application step before them.
	"application_required": contractshttp.StatusConflict,
	// 409 too: the request is well formed, and what refuses it is the create
	// an operator is still deciding.
	"submission_held": contractshttp.StatusConflict,
	// 409 as an unlink answers them: a retry with a new customer waits until
	// no money can move at the provider on the customer it would leave.
	"virtual_account_active":   contractshttp.StatusConflict,
	"transaction_open":         contractshttp.StatusConflict,
	"provisioning_in_flight":   contractshttp.StatusConflict,
	"estimate_expired":         contractshttp.StatusConflict,
	"quote_expired_settlement": contractshttp.StatusConflict,
	"estimate_consumed":        contractshttp.StatusConflict,
	"estimate_mismatch":        contractshttp.StatusConflict,
	"sandbox_only":             contractshttp.StatusConflict,
	"invalid_order_state":      contractshttp.StatusConflict,
	"estimate_required":        contractshttp.StatusUnprocessableEntity,
	"email_mismatch":           contractshttp.StatusUnprocessableEntity,
	// 409 like tax_id_in_use: the company e-mail is well formed, and what
	// refuses it is another customer the provider holds it for, which is
	// never adopted (KYB spec §8). The app reopens the company step.
	"email_in_use": contractshttp.StatusConflict,
	// 422 like a failed rule: the provider refused a document as it was sent,
	// which the user sends again. Mostly the worker records it; a synchronous
	// path that ever meets it answers the same.
	"document_unreadable": contractshttp.StatusUnprocessableEntity,
	// 409, not 422: the fields are well formed, and what refuses them is the
	// state of another account — the CPF or phone it already holds. The app
	// tells them apart by code: a CPF sends the user to support, a phone back
	// to its field.
	"tax_id_in_use": contractshttp.StatusConflict,
	"phone_in_use":  contractshttp.StatusConflict,
	// 409 like a duplicate Pix key: the account is well formed and the user
	// already saved it.
	"bank_account_already_exists": contractshttp.StatusConflict,
	// 409 like tax_id_in_use: another business past draft took the draft's
	// document since it was opened (contract §5.4).
	"business_exists": contractshttp.StatusConflict,
	// A person registers no wallet (POST /api/wallets): what the form
	// requests answer a caller who may not do this at all.
	"forbidden": contractshttp.StatusForbidden,
	// 422 like a failed rule: both are pairings between fields of the body
	// that the form request cannot express, and the codes are what the apps
	// read.
	"documents_required":     contractshttp.StatusUnprocessableEntity,
	"document_type_required": contractshttp.StatusUnprocessableEntity,
	"not_found":              contractshttp.StatusNotFound,
	// 404 beside not_found, under its own code: the body names a bank
	// beneficiary that is not the customer's or does not exist.
	"bank_beneficiary_not_found": contractshttp.StatusNotFound,
	// 409: the account is registered and well formed, and the provider has not
	// approved it yet. Asking again later is the whole remedy.
	"bank_account_verifying": contractshttp.StatusConflict,
	// 422: the provider refused the account as registered, and the user
	// corrects the beneficiary.
	"bank_account_rejected": contractshttp.StatusUnprocessableEntity,
	// 409 like estimate_mismatch: the create is well formed, and what refuses
	// it is the beneficiary, edited since the quote. The app quotes again.
	"beneficiary_changed": contractshttp.StatusConflict,
	// 409: the order has no funding action to refresh (its quote lapsed, or
	// it is not a bank payout awaiting its funds). The app reads the order.
	"funding_action_expired": contractshttp.StatusConflict,
	// 422 like a failed rule: the wallet signed something other than what was
	// prepared, and signs again. 409 for a key replayed with another body, the
	// state refusing it; 422 for a missing key, a field of the request.
	"invalid_signature":        contractshttp.StatusUnprocessableEntity,
	"idempotency_key_reused":   contractshttp.StatusConflict,
	"idempotency_key_required": contractshttp.StatusUnprocessableEntity,
	"amount_below_minimum":     contractshttp.StatusUnprocessableEntity,
	"amount_above_maximum":     contractshttp.StatusUnprocessableEntity,
	// 422 like a failed rule: the address the user typed is not one on its
	// chain, refused before any provider is asked. The user retypes it.
	"invalid_wallet": contractshttp.StatusUnprocessableEntity,
	// 422 like a failed rule: the request is refused before any provider is
	// asked, over what it declares — a residence or tax identity nobody
	// enabled serves — which the user can change (a CPF opens what a
	// passport does not), not over the state of anything.
	"jurisdiction_not_served": contractshttp.StatusUnprocessableEntity,
	// Same status as no_provider_available and a different code on purpose:
	// the two produce the same screen but different investigations, and a rail
	// an operator closed must not read as a rail nobody configured.
	"rail_disabled":           contractshttp.StatusServiceUnavailable,
	"no_provider_available":   contractshttp.StatusServiceUnavailable,
	"documents_not_supported": contractshttp.StatusConflict,
	// 422 like a failed rule: the adapter checked the application or the
	// documents against what its provider's create needs, before any call.
	"kyc_data_invalid": contractshttp.StatusUnprocessableEntity,
	// 503, not 502: the provider answered, it is just busy. The status is
	// what tells a client library the call is worth repeating.
	"provider_busy": contractshttp.StatusServiceUnavailable,
	// 503 for the same reason no_provider_available is: the KYC step the app
	// asked for cannot be served right now, and the app has a fallback.
	"hosted_link_unavailable": contractshttp.StatusServiceUnavailable,
	// 409, not 503: nothing is unavailable and retrying never helps. The
	// provider verifies from the customer record, so the in-app document flow
	// is the path — which is what the client already does when it is not sent
	// to a hosted page.
	"verification_not_supported": contractshttp.StatusConflict,
	// 422 like a failed rule: the bank account is well formed and saved, and
	// the provider said it does not take the rail asked. The user picks the
	// other rail or another account.
	"bank_account_rail_unsupported": contractshttp.StatusUnprocessableEntity,
	// 422 like a money rule: the request is well formed, and the customer it
	// acts for (a company) has no USD payout address yet.
	"business_payout_unsupported": contractshttp.StatusUnprocessableEntity,
	// 401 like Auth's answer to a token whose account is gone: the account
	// was deleted while this request was running, and the write that would
	// have put money in flight for it refused.
	"unauthenticated": contractshttp.StatusUnauthorized,
	// 404: the customer header names no customer the user is a member of
	// (middleware.CustomerScope answers it before any handler).
	"customer_not_found": contractshttp.StatusNotFound,
	// 409: the customer exists and is the user's, and its state — closed —
	// refuses moving money (middleware.RefuseClosedCustomer).
	"customer_closed": contractshttp.StatusConflict,
	// 409: the address is well formed, and another holder holds it; the app
	// creates a new wallet for the company (API contract §9).
	"wallet_in_use": contractshttp.StatusConflict,
	// 422 like a failed rule: a business's own address and own CNPJ key are
	// pairings of the body with the company that the form request cannot
	// express (spec §9, API contract §10). The app sends the user to register
	// the wallet, or to the company's own key.
	"wallet_not_registered": contractshttp.StatusUnprocessableEntity,
	"holder_mismatch":       contractshttp.StatusUnprocessableEntity,
}

// statusFor is the status an error answers with, given the code ErrorCode
// resolved for it. A code errorStatus does not list is the generic one, and
// only a failure the provider owns may answer with it: that is a 502, and
// providerFailed says whether the provider owns it. Anything else is ours — a
// database, a cache, a bug — and ok is false so the caller answers 500, which
// is what the internal-error alert watches.
func statusFor(ctx contractshttp.Context, err error, code, action string, providerFailed bool) (status int, ok bool) {
	if status, ok := errorStatus[code]; ok {
		return status, true
	}
	if !providerFailed {
		return 0, false
	}
	facades.Log().WithContext(ctx).Errorf("ramps: %s: %v", action, err)
	return contractshttp.StatusBadGateway, true
}

// mapError turns a service error into the client contract.
//
// The mapping is by sentinel, never by string: the client branches on these
// codes, and matching on a message would break the app the first time an error
// is reworded. Upstream messages can restate customer data and never reach a
// response.
//
// The sentinel matching itself lives in resources.ErrorCode, not here: the
// fan-out's SubmissionResource needs the exact same code for the exact same
// error, and calling that one function is what keeps the two response shapes
// from drifting apart on what a given failure is called.
func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	// A business application's fields refused past the form request answer
	// in the validation shape, the rule key the app maps (contract §5.2).
	var refusal *kycsvc.FieldRefusal
	if errors.As(err, &refusal) {
		return ctx.Response().Status(contractshttp.StatusUnprocessableEntity).
			Json(responses.ValidationErrorResponse{Error: refusal.Code, Errors: refusal.Fields})
	}
	code := resources.ErrorCode(err)
	status, ok := statusFor(ctx, err, code, action, errors.Is(err, ramp.ErrProviderFailed))
	if !ok {
		return responses.InternalError(ctx, fmt.Errorf("ramps: %s: %w", action, err))
	}
	// A compliance refusal names who refused and what they still want, so the
	// app can open that one provider's wizard instead of the union of
	// everybody's steps. Every other error answers with the code alone.
	var blocked *ramp.KycBlocked
	if errors.As(err, &blocked) {
		return ctx.Response().Status(status).Json(responses.ErrorResponse{
			Error:        code,
			Provider:     blocked.Provider,
			PendingSteps: blocked.PendingSteps,
		})
	}
	// A limit refusal names the bound it refused against, so the app can tell
	// the user the number instead of sending them back to guess it. LimitToken
	// is empty where there was no quote to convert on; omitempty drops it.
	var violation *ramp.LimitViolation
	if errors.As(err, &violation) {
		return ctx.Response().Status(status).Json(responses.ErrorResponse{
			Error:      code,
			LimitBRL:   violation.LimitBRL,
			LimitToken: violation.LimitToken,
			LimitUSD:   violation.LimitUSD,
		})
	}
	return responses.Error(ctx, status, code)
}

// fanOutResponse answers a fan-out with the per-provider list, whatever the
// status.
//
// submitToEnabled hands back both the outcomes and the first refusal's error.
// Passing that error to mapError and dropping the list loses the per-provider
// detail in exactly the case where the client needs it most: when nobody
// accepted, one code cannot say which provider refused or why, and the app has
// nothing useful to render.
//
// The status still comes from that first error, through the same table
// mapError uses, so a caller reading only the code sees no change. Only when
// every outcome is ours does the list have no story to tell: then, and only
// then, it answers 500 alone.
func fanOutResponse(
	ctx contractshttp.Context,
	outcomes []kycsvc.SubmissionOutcome,
	err error,
	action string,
) contractshttp.Response {
	body := resources.SubmissionListResponse{Submissions: resources.NewSubmissionCollection(outcomes)}
	if err == nil {
		// 202, not 201: no provider has seen this yet.
		return ctx.Response().Status(contractshttp.StatusAccepted).Json(body)
	}
	code := resources.ErrorCode(err)
	// The first refusal may be ours while a later one is still a provider's,
	// and that one has a story the app can render. Which rail runs first must
	// not decide the status, so the list is what is asked, not the first
	// error alone.
	providerFailed := errors.Is(err, ramp.ErrProviderFailed) ||
		slices.ContainsFunc(outcomes, func(outcome kycsvc.SubmissionOutcome) bool {
			return errors.Is(outcome.Err, ramp.ErrProviderFailed)
		})
	status, ok := statusFor(ctx, err, code, action, providerFailed)
	if !ok {
		return responses.InternalError(ctx, fmt.Errorf("ramps: %s: %w", action, err))
	}
	body.Error = code
	return ctx.Response().Status(status).Json(body)
}
