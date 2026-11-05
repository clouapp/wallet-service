// Package ramp defines the provider-agnostic contract for PIX on/off-ramps.
//
// An adapter is a pure HTTP mapper: it translates these types to and from one
// provider's API and never touches the database. Everything an adapter needs
// about the customer arrives in a CustomerRef the service layer resolved from
// `customer_provider_integrations`, which is what keeps adapter tests to `httptest`
// alone. Persistence, idempotency bookkeeping, estimate expiry and provider
// selection all belong to the services and the router.
package ramp

import (
	"context"
	"slices"
	"strings"

	rampflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ProviderCode identifies a provider. The values are declared in `app/models`
// because the persisted rows need them and `models` must never import `ramp`.
type ProviderCode = string

const (
	ProviderBridge    ProviderCode = models.ProviderBridge
	ProviderMutualPay ProviderCode = models.ProviderMutualPay
	ProviderOzav      ProviderCode = models.ProviderOzav
	ProviderBlindPay  ProviderCode = models.ProviderBlindPay
)

// Rail is one direction of one payment network. It keys the routing settings
// and decides which adapter methods apply.
type Rail string

const (
	RailPixPayin   Rail = Rail(models.RailPixPayin)
	RailPixPayout  Rail = Rail(models.RailPixPayout)
	RailACHPayout  Rail = Rail(models.RailACHPayout)
	RailWirePayout Rail = Rail(models.RailWirePayout)

	RailUSDPayin      Rail = Rail(models.RailUSDPayin)
	RailUSDACHPayout  Rail = Rail(models.RailUSDACHPayout)
	RailUSDWirePayout Rail = Rail(models.RailUSDWirePayout)
)

// PixRails is the closed set of Pix rails, in the order the capabilities read
// model lists them: models.PixRails, typed. A fresh slice each call, so no
// caller can change the vocabulary for the others.
//
// It deliberately excludes the bank rails. Every caller walking it today —
// capabilities, eligibility, jurisdiction, the admin health — answers a Pix
// question, and a bank rail showing up there before its provider exists would
// advertise a rail nobody can serve.
func PixRails() []Rail { return typedRails(models.PixRails()) }

// BankRails is the closed set of bank payout rails, ACH before wire.
func BankRails() []Rail { return typedRails(models.BankRails()) }

func typedRails(names []string) []Rail {
	rails := make([]Rail, len(names))
	for i, name := range names {
		rails[i] = Rail(name)
	}
	return rails
}

// USDRails is models.USDRails, typed. A fresh slice each call.
func USDRails() []Rail { return typedRails(models.USDRails()) }

// AllRails is the rails a read model walks, Pix then USD: models.AllRails,
// typed. It excludes BankRails, for the reason PixRails explains. A fresh slice
// each call.
func AllRails() []Rail { return typedRails(models.AllRails()) }

// String renders the rail for persistence and log fields.
func (r Rail) String() string { return string(r) }

// IsBank reports whether the rail pays out to a bank account rather than a
// Pix key. Everything that differs between the two — the destination a
// request carries, the currency limits are in — branches on this.
func (r Rail) IsBank() bool {
	return slices.Contains(BankRails(), r)
}

// FiatCurrency is the fiat the rail settles in: "brl", "usd", or "" for a
// rail the stack does not know.
func (r Rail) FiatCurrency() string { return models.RailFiatCurrency(string(r)) }

// IsUSDPayout reports whether the rail is one of the USD payout rails served
// through a Bridge liquidation address: ACH or wire. It is not IsBank, which
// names the BlindPay bank payout rails.
func (r Rail) IsUSDPayout() bool { return r == RailUSDACHPayout || r == RailUSDWirePayout }

// PayinRailFor is the rail a standing account collecting currency settles on:
// brl on pix_payin, usd on usd_payin. Case-insensitive. A currency with no
// rail answers false and is never assumed onto a default.
func PayinRailFor(currency string) (Rail, bool) {
	switch strings.ToLower(strings.TrimSpace(currency)) {
	case models.FiatCurrencyBRL:
		return RailPixPayin, true
	case models.FiatCurrencyUSD:
		return RailUSDPayin, true
	}
	return "", false
}

// Flag names the feature flag that closes the rail to new operations, globally
// or for one user. A rail with no switch names none, and reading "" fails —
// which every caller treats as closed: a rail nobody gave a switch is a rail
// nobody decided to open.
//
// It is read where something is born — an estimate, a create, a standing
// account, a deposit turning into a transaction — and deliberately nowhere
// else. Reading an order, refreshing its status or advancing an existing row
// never asks, so an order that exists finishes even while its rail is closed.
//
// Both bank rails share one switch: they are one product with two settlement
// speeds, and closing one without the other is not a decision anyone needs.
func (r Rail) Flag() string {
	switch r {
	case RailPixPayin:
		return rampflags.PayinEnabled
	case RailPixPayout:
		return rampflags.PayoutEnabled
	case RailACHPayout, RailWirePayout:
		return rampflags.BankPayoutEnabled
	case RailUSDPayin:
		return rampflags.USDPayinEnabled
	case RailUSDACHPayout, RailUSDWirePayout:
		return rampflags.USDPayoutEnabled
	}
	return ""
}

// Capability is one thing a user may ask the stack to do. It is the key
// eligibility is decided on, replacing the bare rail.
//
// Token and Chain are both optional, and an empty one means "the caller did not
// ask". That is not a loophole, it is the shape two real callers take: the
// deposit-first on-ramp collects BRL into a standing account and picks the
// network at delivery rather than at request time, and the sticky create path
// resolves on the rail alone because the token was already settled by the
// estimate it redeems. An adapter that genuinely requires one declares that by
// refusing the capability that omits it.
//
// The rail is what the admin's order and the endorsements are keyed by; token
// and chain are what the adapter knows how to price and settle. Keeping all
// three in one value is what lets the eligibility chain answer "is there
// anyone who serves THIS" instead of "is there anyone on this rail".
type Capability struct {
	Rail  Rail   // pix_payin | pix_payout | usd_payin | usd_ach_payout | usd_wire_payout
	Token string // USDC, USDT — the client vocabulary, matched case-insensitively
	Chain string // solana — empty means "the provider's default"
	// PreparedFunding is the caller declaring it can sign the funding of a
	// payout the provider prepared. Only pix_payout reads it: there a provider
	// that prepares funding is a choice the app must have asked for (D13).
	PreparedFunding bool
}

// KycMode is how a provider prefers to collect identity documents.
type KycMode string

const (
	KycModeAPIDocs            KycMode = KycMode(models.KycModeAPIDocs)
	KycModeHostedVerification KycMode = KycMode(models.KycModeHostedVerification)
)

// String renders the mode for persistence.
func (m KycMode) String() string { return string(m) }

// CollectionMode is the mode actually in use for a given user. It is a
// superset of KycMode: `hosted_fallback` records that the API-docs path was
// the preference but a hosted link had to be issued anyway.
type CollectionMode string

const (
	CollectionModeAPIDocs            CollectionMode = CollectionMode(models.KycModeAPIDocs)
	CollectionModeHostedVerification CollectionMode = CollectionMode(models.KycModeHostedVerification)
	CollectionModeHostedFallback     CollectionMode = CollectionMode(models.KycModeHostedFallback)
)

// String renders the mode for persistence.
func (m CollectionMode) String() string { return string(m) }

// Endorsement statuses, canonical across providers.
const (
	EndorsementStatusApproved   = models.EndorsementStatusApproved
	EndorsementStatusPending    = models.EndorsementStatusPending
	EndorsementStatusIncomplete = models.EndorsementStatusIncomplete
	EndorsementStatusRevoked    = models.EndorsementStatusRevoked
)

// CustomerRef is the provider-side identity the service layer resolved from
// the customer's integration row. Adapters never query the database.
type CustomerRef struct {
	// CustomerID is the XIP customer the call is for, carried for logging and
	// idempotency derivation only — an adapter must never use it as a
	// provider-side identifier.
	CustomerID uint
	// ExternalID is the provider's customer id, empty before the first submit
	// materializes one.
	ExternalID string
}

// NewCustomerRef builds a reference from the nullable column the integration
// row carries. Every service resolves it the same way, so the nil handling
// lives here rather than being written out at each call site.
func NewCustomerRef(customerID uint, externalID *string) CustomerRef {
	ref := CustomerRef{CustomerID: customerID}
	if externalID != nil {
		ref.ExternalID = *externalID
	}
	return ref
}

// KycAdapter is one provider's compliance flow.
type KycAdapter interface {
	Code() ProviderCode
	// PreferredKycMode is how this provider would rather collect documents.
	// The client uses it to choose the UX; the service may still fall back.
	PreferredKycMode() KycMode
	// RequiresTos reports whether this provider has terms of service of its
	// own. A provider without them has no ToS step: the service must skip it
	// rather than treat the absence as a failure, and must not gate the
	// application on a signed agreement id that can never exist.
	RequiresTos() bool
	CreateTosSession(ctx context.Context, ref CustomerRef, opts TosOpts) (*TosSession, error)
	SubmitApplication(ctx context.Context, ref CustomerRef, app Application) (*KycState, error)
	SubmitDocuments(ctx context.Context, ref CustomerRef, docs Documents) (*KycState, error)
	GetKyc(ctx context.Context, ref CustomerRef) (*KycState, error)
	RefreshKyc(ctx context.Context, ref CustomerRef) (*KycState, error)
	GetRequirements(ctx context.Context, ref CustomerRef) (*KycRequirements, error)
	RequestEndorsements(ctx context.Context, ref CustomerRef, endorsements []string) (*KycState, error)
	CreateHostedKycLink(ctx context.Context, ref CustomerRef, opts HostedLinkOpts) (*HostedKycLink, error)
}

// KycSimulator is a provider whose sandbox can approve a customer without a
// real review (Bridge). It is optional: the admin service type-asserts it, and
// an adapter without it is refused as ErrSandboxOnly.
type KycSimulator interface {
	SimulateKycApproval(ctx context.Context, ref CustomerRef) error
}

// RampAdapter is one provider's money movement for the supported rails.
type RampAdapter interface {
	Code() ProviderCode
	// Supports reports whether this provider serves the whole capability, not
	// only its rail. The token filter lives here rather than inside the quote
	// call because the router has to know, before it picks anyone, which
	// provider would have served what the user asked for — otherwise a token
	// only one provider prices reads as a token nobody prices.
	Supports(cap Capability) bool
	EstimateOnramp(ctx context.Context, ref CustomerRef, req OnrampQuoteRequest) (*Estimate, error)
	CreateOnramp(ctx context.Context, ref CustomerRef, req OnrampCreateRequest) (*RampOrder, error)
	EstimateOfframp(ctx context.Context, ref CustomerRef, req OfframpQuoteRequest) (*Estimate, error)
	CreateOfframp(ctx context.Context, ref CustomerRef, req OfframpCreateRequest) (*RampOrder, error)
	// GetOrder re-reads an order from the provider. Webhooks alone are not a
	// status machine — a dropped delivery would leave an order in
	// `awaiting_funds` forever — so both the on-demand read and the
	// reconciliation sweep pull through here.
	GetOrder(ctx context.Context, ref CustomerRef, providerOrderID string) (*RampOrder, error)
	// ParseWebhook verifies the signature over the raw body before decoding it,
	// so a forged payload never reaches a parser.
	ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (*WebhookResult, error)
	// DecodeWebhook maps a payload that has already been verified. It exists for
	// reprocessing: the body comes out of our own `webhook_events` table, which
	// it only entered after ParseWebhook accepted it, and the original headers
	// are long gone. Calling it on anything that arrived from outside would
	// skip the only check that makes a delivery trustworthy.
	DecodeWebhook(ctx context.Context, body []byte) (*WebhookResult, error)
}

// IntegrationAdapter links a local user to a provider-side customer.
type IntegrationAdapter interface {
	Code() ProviderCode
	StartIntegration(ctx context.Context, ref CustomerRef) (*Integration, error)
	RefreshIntegration(ctx context.Context, ref CustomerRef) (*Integration, error)
}

// ProviderAdapter is the full contract a provider package implements. Stubs
// satisfy it too and return ErrProviderNotImplemented.
type ProviderAdapter interface {
	KycAdapter
	RampAdapter
	IntegrationAdapter
}

// PayoutLimitReader is a provider that knows how much a customer may still
// pay out today. Optional, found by type assertion like BankAccountRegistrar.
type PayoutLimitReader interface {
	// RemainingPayoutUSD answers the smaller of the customer's daily and
	// monthly payout allowance, in dollars with two places.
	RemainingPayoutUSD(ctx context.Context, ref CustomerRef) (string, error)
}
