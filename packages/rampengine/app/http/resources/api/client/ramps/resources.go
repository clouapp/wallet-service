// Package ramps shapes the client responses for integrations, KYC and the
// on/off-ramps.
//
// Every resource here is a deliberate narrowing. The models carry columns the
// user must never see — `raw_status` is the provider's snapshot of a person,
// `signed_agreement_id` is contract state, `idempotency_key` is ours — and a
// resource is where that line is drawn once instead of at each handler.
package ramps

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/support/wiretime"
)

// IntegrationResource is one provider link. @name ClientIntegrationResource
type IntegrationResource struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	// Linked reports whether the provider-side customer exists yet, which is
	// the fact the app actually branches on.
	Linked bool `json:"linked"`
} //	@name	ClientIntegrationResource

// NewIntegrationResource projects one link.
func NewIntegrationResource(row *models.CustomerProviderIntegration) IntegrationResource {
	return IntegrationResource{
		Provider: row.Provider,
		Status:   row.Status,
		Linked:   row.ExternalCustomerID != nil && *row.ExternalCustomerID != "",
	}
}

// NewIntegrationCollection projects a list.
func NewIntegrationCollection(rows []models.CustomerProviderIntegration) []IntegrationResource {
	out := make([]IntegrationResource, 0, len(rows))
	for i := range rows {
		out = append(out, NewIntegrationResource(&rows[i]))
	}
	return out
}

// KycResource is a user's compliance state at one provider. @name ClientKycResource
type KycResource struct {
	Provider  string `json:"provider"`
	KycStatus string `json:"kyc_status"`
	TosStatus string `json:"tos_status"`
	// PreferredMode tells the app which flow to offer; CollectionMode says
	// which one is actually in force, so a fallback to the hosted link is
	// visible without a second call.
	PreferredMode  string   `json:"preferred_mode"`
	CollectionMode string   `json:"collection_mode"`
	TosLinkURL     *string  `json:"tos_link_url,omitempty"`
	HostedKycURL   *string  `json:"hosted_kyc_url,omitempty"`
	Endorsements   []string `json:"endorsements"`
	Requirements   []string `json:"requirements"`
	// PendingSteps is Requirements answered as a question the client can act
	// on: which wizard steps the user still owes. The mapping lives on this
	// side, once, because each provider names its outstanding items in its own
	// vocabulary and no client should have to learn any of them.
	//
	// It is also what the fan-out's redistribution filter reads: "is there
	// anything left the user can act on" is the same question for both, and
	// answering it twice is how the two would come to disagree.
	PendingSteps []string `json:"pending_steps"`
	// NeedsApplication is true when the provider holds no customer for this
	// user and nothing is on its way to create one: it owes the whole
	// application, and PendingSteps lists every step of it.
	NeedsApplication bool     `json:"needs_application"`
	RejectionReasons []string `json:"rejection_reasons"`
	// RejectionCategories is why this provider refused the user, in XIP's
	// vocabulary (selfie_mismatch, document_unreadable, data_mismatch,
	// phone_in_use, tax_id_in_use, not_eligible); empty unless the row is
	// rejected. The app owns the text for each. The provider's own words
	// never reach the client.
	RejectionCategories []string `json:"rejection_categories"`
	// Terminal is true when this provider refused the user for good: it will
	// not receive their data again, and nothing in the wizard can change it.
	Terminal     bool       `json:"terminal"`
	RailsOpen    []string   `json:"rails_open"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	// LastError is why the last submission never took, when it did not. The
	// submission runs on a worker after the request has answered 202, so
	// this is the only place the app can still learn the outcome once the
	// event frame that announced it is gone. Absent when the last submission
	// was accepted.
	LastError *KycFailureResource `json:"last_error,omitempty"`
} //	@name	ClientKycResource

// KycFailureResource is a submission the provider never took, by code. The
// upstream text stays on the server: only this side knows whether it restates
// customer data. @name ClientKycFailureResource
type KycFailureResource struct {
	// Code is the same vocabulary as the error codes on the submission
	// endpoints — tos_required, provider_busy, provider_unavailable — so a
	// failure read from the row means what the same code means in a response.
	Code string     `json:"code"`
	At   *time.Time `json:"at,omitempty"`
} //	@name	ClientKycFailureResource

// newKycFailureResource projects the row's last failure, or nil when there
// is none.
func newKycFailureResource(row *models.CustomerProviderKyc) *KycFailureResource {
	if row.LastErrorCode == nil {
		return nil
	}
	return &KycFailureResource{Code: *row.LastErrorCode, At: wiretime.Ptr(row.LastErrorAt)}
}

// NewKycResource projects one KYC row.
//
// `raw_status` and `signed_agreement_id` are absent by construction: the first
// restates the customer's own submitted fields, and the second is contract
// state the app has no use for.
func NewKycResource(row *models.CustomerProviderKyc) KycResource {
	requirements := decodeList(row.Requirements)
	// The same evidence (OutstandingFor) the redistribution filter reads, but
	// the whole list rather than DocumentSteps' narrower one: this answers the
	// wizard's question — which screen to open — and a profile-only list opens
	// the profile step here even though ShouldSkipRedistribution treats that
	// same list as nothing a document resubmission can act on. The two answers
	// differ on purpose.
	pendingSteps := kycsvc.PendingStepsFor(row)
	if pendingSteps == nil {
		pendingSteps = []string{}
	}
	resource := KycResource{
		Provider:            row.Provider,
		KycStatus:           row.KycStatus,
		TosStatus:           row.TosStatus,
		PreferredMode:       row.PreferredMode,
		CollectionMode:      row.CollectionMode,
		TosLinkURL:          row.TosLinkURL,
		HostedKycURL:        row.HostedKycURL,
		Endorsements:        approvedEndorsementNames(row),
		Requirements:        requirements,
		PendingSteps:        pendingSteps,
		NeedsApplication:    row.NeedsApplication,
		RejectionReasons:    decodeList(row.RejectionReasons),
		RejectionCategories: decodeList(row.RejectionCategories),
		Terminal:            kycsvc.Terminal(row),
		RailsOpen:           OpenRails(row),
		LastSyncedAt:        wiretime.Ptr(row.LastSyncedAt),
		LastError:           newKycFailureResource(row),
	}
	return resource
}

// OverviewResource is GET /api/kyc/overview: the one state the user sees, and
// the per-provider list it was read from — the same list GET /api/kyc
// answers. @name ClientKycOverviewResource
type OverviewResource struct {
	Overall   OverallResource `json:"overall"`
	Providers []KycResource   `json:"providers"`
} //	@name	ClientKycOverviewResource

// OverallResource is the user's KYC across every eligible provider. @name ClientKycOverallResource
type OverallResource struct {
	Status string `json:"status" enums:"not_started,in_review,action_required,approved,rejected,unavailable"`
	// Categories is empty unless the status is action_required (the
	// corrigible categories of the providers waiting on the user) or rejected
	// (tax_id_in_use or not_eligible).
	Categories []string `json:"categories"`
	// PendingSteps is empty unless the status is not_started or
	// action_required: the wizard steps to open, in wizard order.
	PendingSteps []string  `json:"pending_steps"`
	CheckedAt    time.Time `json:"checked_at"`
	// PendingDocuments is the company documents a business's providers still
	// name (formation, proof_of_address, source_of_funds,
	// tax_identification, nature_of_business, in that order): non-empty only while
	// company_documents is pending and a provider said which. Always empty
	// for a person.
	PendingDocuments []string `json:"pending_documents"`
} //	@name	ClientKycOverallResource

// NewOverviewResource projects the overview.
func NewOverviewResource(view *kycsvc.OverviewView) OverviewResource {
	return OverviewResource{
		Overall: OverallResource{
			Status:           view.Overall.Status,
			Categories:       nonNil(view.Overall.Categories),
			PendingSteps:     nonNil(view.Overall.PendingSteps),
			CheckedAt:        wiretime.Of(view.CheckedAt),
			PendingDocuments: nonNil(view.Overall.PendingDocuments),
		},
		Providers: NewKycCollection(view.Providers),
	}
}

// nonNil keeps an empty list from serialising as null.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// NewKycCollection projects a list.
func NewKycCollection(rows []models.CustomerProviderKyc) []KycResource {
	out := make([]KycResource, 0, len(rows))
	for i := range rows {
		out = append(out, NewKycResource(&rows[i]))
	}
	return out
}

// SubmissionListResponse is the shape of POST /api/kyc/applications.
//
//	@name	RampSubmissionListResponse
type SubmissionListResponse struct {
	Submissions []SubmissionResource `json:"submissions"`
	// Error is the same stable code the status line carries, repeated where
	// every other endpoint in this API puts it. Without it a client's shared
	// error interceptor reads `error` on a non-2xx, finds nothing, and renders
	// nothing — the per-provider detail below is an addition to that contract,
	// not a replacement for it.
	Error string `json:"error,omitempty"`
} //	@name	RampSubmissionListResponse

// SubmissionResource is one provider's answer to a fan-out submission. The
// error is a stable code, never the upstream message: those restate customer
// data.
//
// Three outcomes, not two. A provider can take the submission, refuse it, or
// have had nothing to do with it — and the third is not a quieter version of
// either. Reported as a refusal it puts an error on screen for a request where
// nothing went wrong; reported as an acceptance it claims a job was queued that
// never was.
type SubmissionResource struct {
	Provider string `json:"provider"`
	Accepted bool   `json:"accepted"`
	// Skipped means this provider owed the user nothing, so the submission was
	// not sent to it. Never accompanied by an error.
	Skipped bool         `json:"skipped,omitempty"`
	Kyc     *KycResource `json:"kyc,omitempty"`
	Error   string       `json:"error,omitempty"`
} //	@name	RampSubmissionResource

// NewSubmissionCollection projects a fan-out's outcomes.
//
// The error, when present, goes through ErrorCode — the same table
// mapError uses in the controllers package — so a refusal reported here and
// one reported through the single-provider {provider} route never disagree on
// what a given failure is called.
func NewSubmissionCollection(outcomes []kycsvc.SubmissionOutcome) []SubmissionResource {
	out := make([]SubmissionResource, 0, len(outcomes))
	for _, outcome := range outcomes {
		resource := SubmissionResource{
			Provider: outcome.Provider,
			Accepted: outcome.Accepted(),
			Skipped:  outcome.Skipped,
		}
		if outcome.Row != nil {
			kyc := NewKycResource(outcome.Row)
			resource.Kyc = &kyc
		}
		if outcome.Err != nil {
			resource.Error = ErrorCode(outcome.Err)
		}
		out = append(out, resource)
	}
	return out
}

// RequirementsResource is what the user still owes. @name ClientKycRequirementsResource
type RequirementsResource struct {
	Provider     string   `json:"provider"`
	Status       string   `json:"status"`
	Missing      []string `json:"missing"`
	PendingSteps []string `json:"pending_steps"`
	Endorsements []string `json:"endorsements"`
	// NeedsApplication: no customer at the provider yet — pending_steps is
	// the whole application.
	NeedsApplication bool `json:"needs_application"`
} //	@name	ClientKycRequirementsResource

// NewRequirementsResource projects the service's view. The provider's raw
// snapshot stops at the service — see kyc.RequirementsView — so there is
// nothing to strip here.
func NewRequirementsResource(view *kycsvc.RequirementsView) RequirementsResource {
	names := make([]string, 0, len(view.Endorsements))
	for _, endorsement := range view.Endorsements {
		names = append(names, endorsement.Name)
	}
	missing := view.Missing
	if missing == nil {
		missing = []string{}
	}
	pendingSteps := view.PendingSteps
	if pendingSteps == nil {
		pendingSteps = []string{}
	}
	return RequirementsResource{
		Provider:         view.Provider,
		Status:           view.Status,
		Missing:          missing,
		PendingSteps:     pendingSteps,
		Endorsements:     names,
		NeedsApplication: view.NeedsApplication,
	}
}

// EstimateResource is a quote. @name ClientRampEstimateResource
type EstimateResource struct {
	EstimateID       string `json:"estimate_id"`
	Rail             string `json:"rail"`
	Rate             string `json:"rate"`
	FeePercent       string `json:"fee_percent"`
	FeeAmount        string `json:"fee_amount"`
	FeeCurrency      string `json:"fee_currency"`
	SourceAmount     string `json:"source_amount"`
	EstimatedReceive string `json:"estimated_receive"`
	// ProviderDeductedAmount is what the provider took from the source leg
	// before converting (network and Pix). Already reflected in
	// estimated_receive; empty when the provider states none.
	ProviderDeductedAmount   string `json:"provider_deducted_amount,omitempty"`
	ProviderDeductedCurrency string `json:"provider_deducted_currency,omitempty"`
	// ProviderFeePercent and ProviderFeeAmount are what the provider's own
	// conversion takes beyond fee_percent (XIP's): a percent, and its value in
	// reais. Absent when the provider names no gap.
	ProviderFeePercent string     `json:"provider_fee_percent,omitempty"`
	ProviderFeeAmount  string     `json:"provider_fee_amount,omitempty"`
	Binding            bool       `json:"binding"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	// DepositMode is how an off-ramp quote's order will be funded: "simulated"
	// (a sandbox provider; nothing is sent on chain) or "on_chain". Absent on
	// an on-ramp quote.
	DepositMode string `json:"deposit_mode,omitempty"`
	// DepositToAddress is where an on-chain off-ramp's deposit is expected to
	// go, known from the user's earlier orders: the provider gives a customer
	// one address. Absent before the first order, when the deposit also
	// creates the address's token account and the sender pays its rent.
	DepositToAddress string `json:"deposit_to_address,omitempty"`
	// DestinationCurrency is the fiat the payout lands in: "brl" on Pix,
	// "usd" on a bank rail. Absent on an on-ramp quote.
	DestinationCurrency string `json:"destination_currency,omitempty"`
} //	@name	ClientRampEstimateResource

// NewEstimateResource projects a quote. The provider is deliberately absent:
// which one served the rail is XIP's routing decision, and exposing it would
// make an internal choice part of the API contract.
func NewEstimateResource(view *clientramps.EstimateView) EstimateResource {
	return EstimateResource{
		EstimateID:               view.EstimateID,
		Rail:                     view.Rail,
		Rate:                     view.Rate,
		FeePercent:               view.FeePercent,
		FeeAmount:                view.FeeAmount,
		FeeCurrency:              view.FeeCurrency,
		SourceAmount:             view.SourceAmount,
		EstimatedReceive:         view.EstimatedReceive,
		ProviderDeductedAmount:   view.ProviderDeductedAmount,
		ProviderDeductedCurrency: view.ProviderDeductedCurrency,
		ProviderFeePercent:       view.ProviderFeePercent,
		ProviderFeeAmount:        view.ProviderFeeAmount,
		Binding:                  view.Binding,
		ExpiresAt:                wiretime.Ptr(view.ExpiresAt),
		DepositMode:              view.DepositMode,
		DepositToAddress:         view.DepositToAddress,
		DestinationCurrency:      view.DestinationCurrency,
	}
}

// BankRailOptionResource is one bank rail on the options screen.
type BankRailOptionResource struct {
	Rail      string `json:"rail"`
	Available bool   `json:"available"`
	// Reason is why the rail is not offered: flag_closed, tos_required,
	// kyc_required or provider_unavailable. Absent when it is.
	Reason string   `json:"reason,omitempty"`
	Tokens []string `json:"tokens"`
	Chain  string   `json:"chain,omitempty"`
	// MinUSD and MaxUSD bound the payout in dollars of the source stablecoin.
	// MaxUSD is absent where nothing bounds it.
	MinUSD string `json:"min_usd,omitempty"`
	MaxUSD string `json:"max_usd,omitempty"`
	// RequiresSolForFee tells the app the customer's wallet must hold SOL: the
	// payout's delegation transaction is paid by the owner, not by us. False
	// when the rail is not offered. Added with the endpoint, so additive.
	RequiresSolForFee bool `json:"requires_sol_for_fee" example:"true"`
} //	@name	ClientBankRailOptionResource

// BankOptionsResource is what the bank payout screen shows before a quote.
type BankOptionsResource struct {
	Rails []BankRailOptionResource `json:"rails"`
} //	@name	ClientBankOptionsResource

// NewBankOptionsResource projects the options.
func NewBankOptionsResource(view *clientramps.BankOptionsView) BankOptionsResource {
	rails := make([]BankRailOptionResource, len(view.Rails))
	for i, option := range view.Rails {
		rails[i] = BankRailOptionResource{
			Rail: option.Rail, Available: option.Available, Reason: option.Reason,
			Tokens: append([]string{}, option.Tokens...), Chain: option.Chain,
			MinUSD: option.MinUSD, MaxUSD: option.MaxUSD,
			RequiresSolForFee: option.RequiresSolForFee,
		}
	}
	return BankOptionsResource{Rails: rails}
}

// OrderResource is a ramp order. @name ClientRampOrderResource
type OrderResource struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Rail   string `json:"rail"`
	Status string `json:"status"`
	// AmountBRL is the fiat leg of a BRL order only; a USD order leaves it
	// out and carries amount_fiat.
	AmountBRL   string `json:"amount_brl,omitempty"`
	AmountToken string `json:"amount_token"`
	Token       string `json:"token"`
	Rate        string `json:"rate,omitempty"`
	// ExchangeRate is the provider's settled conversion rate, when known.
	ExchangeRate      string `json:"exchange_rate,omitempty"`
	EstimatedReceive  string `json:"estimated_receive,omitempty"`
	FeePercent        string `json:"fee_percent,omitempty"`
	FeeAmount         string `json:"fee_amount,omitempty"`
	FeeCurrency       string `json:"fee_currency,omitempty"`
	ExchangeFeeAmount string `json:"exchange_fee_amount,omitempty"`
	// ExchangeFeeCurrency is the unit the provider charged its conversion in:
	// the token on an off-ramp, the order's fiat on an on-ramp.
	ExchangeFeeCurrency string `json:"exchange_fee_currency,omitempty"`
	// ProviderFeePercent and ProviderFeeAmount are the conversion gap the
	// order's quote named (see ClientRampEstimateResource).
	ProviderFeePercent string `json:"provider_fee_percent,omitempty"`
	ProviderFeeAmount  string `json:"provider_fee_amount,omitempty"`
	PixKey             string `json:"pix_key,omitempty"`
	PixKeyType         string `json:"pix_key_type,omitempty"`
	RecipientName      string `json:"recipient_name,omitempty"`
	DepositTxSignature string `json:"deposit_tx_signature,omitempty"`
	// DepositInstructions is what the user acts on: a PIX br_code on an
	// on-ramp, a deposit address on an off-ramp.
	DepositInstructions map[string]any `json:"deposit_instructions,omitempty"`
	DepositMode         string         `json:"deposit_mode,omitempty"`
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
	CreatedAt           *time.Time     `json:"created_at,omitempty"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
	FailureReason       string         `json:"failure_reason,omitempty"`
	OnrampPhase         string         `json:"onramp_phase,omitempty"`
	// FiatCurrency is the currency of amount_fiat: "brl" or "usd".
	FiatCurrency string `json:"fiat_currency"`
	AmountFiat   string `json:"amount_fiat,omitempty"`
	// PaymentRail is the bank network that moved the fiat: ach, wire, fednow
	// or pix.
	PaymentRail      string `json:"payment_rail,omitempty"`
	BankAccountLabel string `json:"bank_account_label,omitempty"`
	BankAccountLast4 string `json:"bank_account_last4,omitempty"`
	// The bank payout's fiat leg, on a bank rail only: what lands, repeated
	// from amount_fiat under the name the bank payout contract always used
	// (fiat_amount), and the rail and routing last four it was paid through.
	FiatAmount       string `json:"fiat_amount,omitempty" example:"97.30"`
	BankRail         string `json:"bank_rail,omitempty" example:"ach_payout"`
	BankRoutingLast4 string `json:"bank_routing_last4,omitempty" example:"0021"`
	// FundingAction is what the wallet signs to fund a bank payout, present
	// while the order awaits its funds and the action is valid.
	FundingAction *FundingActionResource `json:"funding_action,omitempty"`
} //	@name	ClientRampOrderResource

// FundingActionResource is a prepared funding action: a Solana transaction
// the wallet signs and returns to the backend (submission_mode
// backend_finalize) before expires_at, naming action_revision.
type FundingActionResource struct {
	Type           string                       `json:"type" example:"solana_transaction"`
	SubmissionMode string                       `json:"submission_mode" example:"backend_finalize"`
	Payload        FundingActionPayloadResource `json:"payload"`
	ExpiresAt      time.Time                    `json:"expires_at"`
	ActionRevision int                          `json:"action_revision" example:"1"`
} //	@name	ClientFundingActionResource

// FundingActionPayloadResource is the unsigned transaction, base64 (std).
type FundingActionPayloadResource struct {
	ContentType         string `json:"content_type" example:"application/x.solana-transaction"`
	UnsignedTransaction string `json:"unsigned_transaction"`
} //	@name	ClientFundingActionPayloadResource

func newFundingActionResource(view *clientramps.FundingActionView) *FundingActionResource {
	if view == nil {
		return nil
	}
	return &FundingActionResource{
		Type: view.Type, SubmissionMode: view.SubmissionMode,
		Payload: FundingActionPayloadResource{
			ContentType: view.ContentType, UnsignedTransaction: view.UnsignedTransaction,
		},
		ExpiresAt:      wiretime.Of(view.ExpiresAt),
		ActionRevision: view.ActionRevision,
	}
}

// bankFiatAmount is fiat_amount: the dollars a bank payout lands, which is the
// order's amount_fiat. A Pix or Bridge USD order leaves it out.
func bankFiatAmount(view *clientramps.OrderView) string {
	if view.BankRail == "" {
		return ""
	}
	return view.AmountFiat
}

// NewOrderResource projects an order.
func NewOrderResource(view *clientramps.OrderView) OrderResource {
	return OrderResource{
		ID:                  view.ID,
		Type:                view.Type,
		Rail:                view.Rail,
		Status:              view.Status,
		AmountBRL:           view.AmountBRL,
		AmountToken:         view.AmountToken,
		Token:               view.Token,
		Rate:                view.Rate,
		ExchangeRate:        view.ExchangeRate,
		EstimatedReceive:    view.EstimatedReceive,
		FeePercent:          view.FeePercent,
		FeeAmount:           view.FeeAmount,
		FeeCurrency:         view.FeeCurrency,
		ExchangeFeeAmount:   view.ExchangeFeeAmount,
		ExchangeFeeCurrency: view.ExchangeFeeCurrency,
		ProviderFeePercent:  view.ProviderFeePercent,
		ProviderFeeAmount:   view.ProviderFeeAmount,
		PixKey:              view.PixKey,
		PixKeyType:          view.PixKeyType,
		RecipientName:       view.RecipientName,
		DepositTxSignature:  view.DepositTxSignature,
		DepositInstructions: view.DepositInstructions,
		DepositMode:         view.DepositMode,
		ExpiresAt:           wiretime.Ptr(view.ExpiresAt),
		CreatedAt:           wiretime.Ptr(view.CreatedAt),
		CompletedAt:         wiretime.Ptr(view.CompletedAt),
		FailureReason:       view.FailureReason,
		FiatCurrency:        view.FiatCurrency,
		AmountFiat:          view.AmountFiat,
		PaymentRail:         view.PaymentRail,
		BankAccountLabel:    view.BankAccountLabel,
		BankAccountLast4:    view.BankAccountLast4,
		OnrampPhase:         view.OnrampPhase,
		FiatAmount:          bankFiatAmount(view),
		BankRail:            view.BankRail,
		BankRoutingLast4:    view.BankRoutingLast4,
		FundingAction:       newFundingActionResource(view.FundingAction),
	}
}

// VirtualAccountResource is a customer's standing deposit account. @name VirtualAccountResource
type VirtualAccountResource struct {
	Status         string `json:"status"`
	SourceCurrency string `json:"source_currency"`
	Token          string `json:"token"`
	Chain          string `json:"chain"`
	Wallet         string `json:"wallet"`
	FeePercent     string `json:"fee_percent,omitempty"`
	// DepositInstructions is what the user pays into — for PIX the permanent,
	// amount-less br_code and the account holder name. The app shows it as a
	// saved payment target, not as a per-order QR: the user types the amount in
	// their own bank app.
	DepositInstructions map[string]any `json:"deposit_instructions,omitempty"`
} //	@name	VirtualAccountResource

// NewVirtualAccountResource projects the standing account.
//
// `external_account_id` is absent for the same reason the estimate omits the
// provider: it is the provider's identifier for the account, and which provider
// serves a rail is XIP's routing decision rather than part of the API contract.
func NewVirtualAccountResource(view *clientramps.VirtualAccountView) VirtualAccountResource {
	return VirtualAccountResource{
		Status:              view.Status,
		SourceCurrency:      view.SourceCurrency,
		Token:               view.Token,
		Chain:               view.Chain,
		Wallet:              view.Wallet,
		FeePercent:          view.FeePercent,
		DepositInstructions: view.DepositInstructions,
	}
}

// TosSessionResource is a hosted terms-of-service page. @name ClientTosSessionResource
type TosSessionResource struct {
	URL       string `json:"url"`
	TosStatus string `json:"tos_status"`
} //	@name	ClientTosSessionResource

// NewTosSessionResource projects the persisted session.
func NewTosSessionResource(row *models.CustomerProviderKyc) TosSessionResource {
	resource := TosSessionResource{TosStatus: row.TosStatus}
	if row.TosLinkURL != nil {
		resource.URL = *row.TosLinkURL
	}
	return resource
}

// HostedLinkResource is an issued verification page. @name ClientHostedKycLinkResource
type HostedLinkResource struct {
	URL            string `json:"url"`
	CollectionMode string `json:"collection_mode"`
} //	@name	ClientHostedKycLinkResource

// NewHostedLinkResource projects the persisted link.
func NewHostedLinkResource(row *models.CustomerProviderKyc) HostedLinkResource {
	resource := HostedLinkResource{CollectionMode: row.CollectionMode}
	if row.HostedKycURL != nil {
		resource.URL = *row.HostedKycURL
	}
	return resource
}

// approvedEndorsementNames lists the capabilities the provider has granted.
// Pending and revoked ones are omitted: the app renders this as "what you can
// do", and a revoked grant in that list would read as a permission.
func approvedEndorsementNames(row *models.CustomerProviderKyc) []string {
	names := []string{}
	for _, endorsement := range row.DecodedEndorsements() {
		if endorsement.IsApproved() {
			names = append(names, endorsement.Name)
		}
	}
	return names
}

// OpenRails is what the endorsements actually clear, derived rather than
// stored — see the note on CustomerProviderKyc — less the rails the KYC service
// marked as ones this provider does not open to the person (RailsNotEligible):
// the router refuses those as rail_not_eligible whatever the endorsements say,
// and the app gates each rail on this list.
func OpenRails(row *models.CustomerProviderKyc) []string {
	rails := []string{}
	for _, rail := range models.AllRails() {
		if row.SupportsRail(rail) && !slices.Contains(row.RailsNotEligible, rail) {
			rails = append(rails, rail)
		}
	}
	return rails
}

// decodeList reads one of the jsonb string arrays. A malformed value yields an
// empty list: these are advisory messages for the app, and failing a whole
// response over one of them would be the worse outcome.
func decodeList(encoded string) []string {
	if encoded == "" {
		return []string{}
	}
	var list []string
	if err := json.Unmarshal([]byte(encoded), &list); err != nil || list == nil {
		return []string{}
	}
	return list
}

// OrderListResponse is the shape of GET /api/onramps and /api/offramps.
//
//	@name	ClientRampOrderListResponse
type OrderListResponse struct {
	Data []OrderResource `json:"data"`
	Meta PageMeta        `json:"meta"`
} //	@name	ClientRampOrderListResponse

// NewOrderListResponse is one page of orders: GET /api/onramps, /api/offramps
// and /api/transactions answer with it.
func NewOrderListResponse(views []clientramps.OrderView, page, perPage int, total int64) OrderListResponse {
	items := make([]OrderResource, 0, len(views))
	for i := range views {
		items = append(items, NewOrderResource(&views[i]))
	}
	return OrderListResponse{Data: items, Meta: PageMeta{Page: page, PerPage: perPage, Total: total}}
}

// PageMeta is the list window. @name ClientRampPageMeta
type PageMeta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
} //	@name	ClientRampPageMeta

// TosSessionListResponse is the shape of POST /api/kyc/tos-sessions.
//
//	@name	RampTosSessionListResponse
type TosSessionListResponse struct {
	Sessions []TosSessionListItem `json:"sessions"`
} //	@name	RampTosSessionListResponse

// TosSessionListItem is one provider's terms page.
//
// AllowedHosts is a security control, not a hint: the client opens this URL in
// a WebView with a JavaScript bridge attached, and this is the set of hosts it
// may let that WebView reach. It travels with the URL because the provider that
// issued the page is the only thing that knows where the page may go.
type TosSessionListItem struct {
	Provider     string   `json:"provider"`
	URL          string   `json:"url"`
	AllowedHosts []string `json:"allowed_hosts"`
	// Mode is how this provider's terms are accepted, and therefore how the
	// client must present them: `hosted` means the page signs them and posts an
	// agreement id back, `attested` means the page says nothing and the client
	// collects the acceptance itself. A session is only ever listed in a mode
	// the client asked for.
	Mode string `json:"mode"`
} //	@name	RampTosSessionListItem

// NewTosSessionCollection projects the pending terms sessions.
func NewTosSessionCollection(views []kycsvc.TosSessionView) []TosSessionListItem {
	out := make([]TosSessionListItem, 0, len(views))
	for _, view := range views {
		hosts := view.AllowedHosts
		if hosts == nil {
			hosts = []string{}
		}
		out = append(out, TosSessionListItem{
			Provider:     view.Provider,
			URL:          view.URL,
			AllowedHosts: hosts,
			Mode:         view.Mode,
		})
	}
	return out
}
