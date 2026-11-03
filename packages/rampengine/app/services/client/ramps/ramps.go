// Package ramps drives the PIX on/off-ramp order lifecycle.
//
// Both directions live here rather than in an `onramp` and an `offramp`
// package as the plan sketched. They share the estimate lifecycle, the rail
// limit checks, the dedup lock and the idempotency derivation — everything
// that touches money — and splitting that across two packages is how a fix
// lands in one and not the other. The two services stay separate types; only
// the machinery underneath is shared.
package ramps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	pushsvc "github.com/macrowallets/waas/packages/rampengine/app/services/push"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrEstimateNotFound is a create referencing a quote that is not the caller's
// or does not exist. The two cases are deliberately indistinguishable: telling
// them apart would let a caller probe for other users' estimate ids.
var ErrEstimateNotFound = errors.New("ramps: estimate not found")

// ErrOrderNotFound is an order that is not the caller's, does not exist, or
// belongs to the other direction. One error for all three, so a caller cannot
// use the endpoint to learn which.
var ErrOrderNotFound = errors.New("ramps: order not found")

// ErrInvalidOrderState is an order whose status does not allow the requested
// action — e.g. simulate-deposit on something other than awaiting_funds.
var ErrInvalidOrderState = errors.New("ramps: invalid order state")

// ErrAccountDeleted is a reservation — an order's or a standing account's —
// refused because the account was deleted while the request that wanted it
// was running. The repository reads that in the reservation's own transaction,
// so it cannot pass a deletion that has committed (spec §19.2).
var ErrAccountDeleted = repositories.ErrUserDeleted

// MfaStepUpValidator validates a TOTP or backup code for a sensitive action.
//
// Enrolled is part of the contract because the enrollment lives in its own
// table now: "does this subject have MFA" is a lookup, not a column on the
// user, and the caller must not reach past this interface to answer it.
type MfaStepUpValidator interface {
	Enrolled(subject identity.Subject) (bool, error)
	ValidateStepUpCode(ctx context.Context, subject identity.Subject, code, codeType string) error
}

// PendingStepsSource answers what one provider still wants from a customer.
//
// It is a port here rather than a call into the kyc package because the
// dependency only runs one way: kyc imports ramp, never the reverse, and the
// router that raises the refusal must stay ignorant of the wizard's
// vocabulary. What it answers is derived from the stored row — no provider is
// called to explain a refusal the user is already waiting on.
type PendingStepsSource interface {
	PendingSteps(customerID uint, provider string) []string
}

// enrichKycRefusal fills in the steps of a compliance refusal that names a
// provider, leaving every other error exactly as it was.
//
// Only a pending refusal is enriched: a rejection is a dead end, so there is no
// step to name and asking for one would only invite a screen that resolves
// nothing. A nil source returns the error unchanged rather than panicking —
// the refusal without its steps is degraded, and degraded beats a 500 on a
// path the user reached by being honest about their compliance state.
func enrichKycRefusal(source PendingStepsSource, customerID uint, err error) error {
	var blocked *ramp.KycBlocked
	if source == nil || !errors.As(err, &blocked) {
		return err
	}
	if !errors.Is(blocked.Err, ramp.ErrKycPending) || blocked.Provider == "" {
		return err
	}
	blocked.PendingSteps = source.PendingSteps(customerID, blocked.Provider)
	return err
}

// EstimateView is the quote handed back to the client. The provider is not on
// it: which provider served the rail is XIP's routing decision, and exposing
// it would make an internal choice part of the API contract.
type EstimateView struct {
	EstimateID       string
	Rail             string
	Rate             string
	FeePercent       string
	FeeAmount        string
	FeeCurrency      string
	SourceAmount     string
	EstimatedReceive string
	// ProviderDeductedAmount is what the provider took out of the source leg
	// before converting (network and Pix costs), in ProviderDeductedCurrency.
	// It is already out of EstimatedReceive and is informational; empty when
	// the provider states none.
	ProviderDeductedAmount   string
	ProviderDeductedCurrency string
	// ProviderFeePercent and ProviderFeeAmount are what the provider's own
	// conversion takes beyond XIP's fee, as a percent and in reais. Empty when
	// the provider names no gap.
	ProviderFeePercent string
	ProviderFeeAmount  string
	Binding            bool
	ExpiresAt          *time.Time
	// DepositMode is how an off-ramp quoted here will be funded — the mode the
	// order is placed with. Empty on an on-ramp, where the user pays by Pix.
	DepositMode string
	// DepositToAddress is where an on-chain off-ramp's deposit is expected to
	// go: the address the provider gave this user's last order. Empty before
	// the first one, and on any other quote.
	DepositToAddress string
	// DestinationCurrency is the fiat an off-ramp pays out in: "brl" on Pix,
	// "usd" on a bank rail. Empty on an on-ramp.
	DestinationCurrency string
}

// OrderView is a created ramp order.
type OrderView struct {
	ID                string
	Type              string
	Rail              string
	Status            string
	AmountBRL         string
	AmountToken       string
	Token             string
	Rate              string
	ExchangeRate      string
	EstimatedReceive  string
	FeePercent        string
	FeeAmount         string
	FeeCurrency       string
	ExchangeFeeAmount string
	// ExchangeFeeCurrency is the unit of ExchangeFeeAmount: the token an
	// off-ramp sends, reais on an on-ramp.
	ExchangeFeeCurrency string
	// ProviderFeePercent and ProviderFeeAmount are the conversion gap the
	// order's quote named.
	ProviderFeePercent  string
	ProviderFeeAmount   string
	PixKey              string
	PixKeyType          string
	RecipientName       string
	DepositTxSignature  string
	DepositInstructions map[string]any
	DepositMode         string
	ExpiresAt           *time.Time
	CreatedAt           *time.Time
	CompletedAt         *time.Time
	FailureReason       string
	OnrampPhase         string
	// FiatCurrency is the currency of AmountFiat, "brl" or "usd". AmountBRL
	// repeats AmountFiat on a BRL order only, for installed apps.
	FiatCurrency string
	AmountFiat   string
	// PaymentRail is the bank network that moved the fiat (ach, wire,
	// fednow, pix), when the provider said.
	PaymentRail string
	// BankAccountLabel and BankAccountLast4 name the US account a USD payout
	// paid into; a bank payout's last fours come from the order itself.
	BankAccountLabel string
	BankAccountLast4 string
	// BankRail and BankRoutingLast4 are the bank payout's rail and the routing
	// number's last four, set only on a bank rail.
	BankRail         string
	BankRoutingLast4 string
	// FundingAction is what the wallet signs to fund a bank payout: set only
	// while the order awaits funds and its prepared action is still valid.
	FundingAction *FundingActionView
}

// FundingActionView is a prepared funding action as the client sees it
// (ramp.FundingAction plus the revision a finalize names).
type FundingActionView struct {
	Type                string
	SubmissionMode      string
	ContentType         string
	UnsignedTransaction string
	ExpiresAt           time.Time
	ActionRevision      int
}

// estimateSnapshot is the canonical quote persisted on `ramp_estimates`.
//
// It is written rather than the provider's raw response because a create that
// redeems the quote has to read back the amounts the user was shown, and every
// provider names them differently. SourceAmount and EstimatedReceive are what
// the rail limits are checked against on an off-ramp, where the BRL leg is
// only knowable through a quote.
type estimateSnapshot struct {
	Rail                     string `json:"rail"`
	SourceAmount             string `json:"source_amount"`
	SourceCurrency           string `json:"source_currency"`
	DestCurrency             string `json:"destination_currency"`
	DestPaymentRail          string `json:"destination_payment_rail"`
	DestAddress              string `json:"destination_address,omitempty"`
	PixKey                   string `json:"pix_key,omitempty"`
	PixKeyType               string `json:"pix_key_type,omitempty"`
	RecipientName            string `json:"recipient_name,omitempty"`
	RecipientDocument        string `json:"recipient_document,omitempty"`
	Chain                    string `json:"chain,omitempty"`
	FromAddress              string `json:"from_address,omitempty"`
	Rate                     string `json:"rate"`
	FeePercent               string `json:"fee_percent"`
	FeeAmount                string `json:"fee_amount"`
	FeeCurrency              string `json:"fee_currency"`
	EstimatedReceive         string `json:"estimated_receive"`
	ProviderDeductedAmount   string `json:"provider_deducted_amount,omitempty"`
	ProviderDeductedCurrency string `json:"provider_deducted_currency,omitempty"`
	// ProviderFeePercent and ProviderFeeAmount are the provider's own
	// conversion gap the quote named (see ramp.Estimate), kept so the order
	// shows it while it is in flight.
	ProviderFeePercent string `json:"provider_fee_percent,omitempty"`
	ProviderFeeAmount  string `json:"provider_fee_amount,omitempty"`
	// The bank payout's destination, as the quote priced it: the beneficiary
	// (external id) and the revision of it the provider account was
	// registered for, that account (ba_), and the provider customer it
	// belongs to. A create compares all four, so a beneficiary edited or an
	// account replaced after the quote is not paid at the quoted terms.
	// Empty on Pix.
	BankBeneficiaryID   string `json:"bank_beneficiary_id,omitempty"`
	BeneficiaryRevision int    `json:"beneficiary_revision"`
	BankAccountID       string `json:"bank_account_id,omitempty"`
	CustomerExternalID  string `json:"customer_external_id,omitempty"`
	// FundingMode is "prepared" when the order is funded by authorization
	// (models.FundingModePrepared), empty when by deposit. On Pix the quote,
	// not the rail, decides it, so the create reads it here.
	FundingMode string `json:"funding_mode,omitempty"`
}

// Deps is the collaborator set every service in this package is built from.
//
// It is one value rather than eleven parameters because the container resolves
// them together and the constructors took them apart again in the same order:
// with four repositories and two *ramp pointers among them, swapping two is a
// change the compiler cannot see.
type Deps struct {
	Router       *ramp.Router
	Config       *ramp.Config
	Transactions repositories.TransactionRepository
	Estimates    repositories.RampEstimateRepository
	Locker       locking.Locker
	Broadcaster  broadcast.Broadcaster
	// Members is who acts for each customer, who hears of its orders.
	Members         repositories.CustomerMemberRepository
	Notifier        pushsvc.Notifier
	Features        contracts.Feature
	PendingSteps    PendingStepsSource
	VirtualAccounts repositories.CustomerVirtualAccountRepository
	Integrations    repositories.CustomerProviderIntegrationRepository
	Factory         *ramp.Factory
	// LiquidationAddresses resolves a USD payout's address when an order in
	// flight is refreshed. Nil leaves those orders as stored.
	LiquidationAddresses repositories.UserLiquidationAddressRepository
	// BankAccounts names the account a USD payout paid (label and last four).
	// Nil leaves those fields blank.
	BankAccounts repositories.UserBankAccountRepository
	// Wallets is the registry of the addresses customers hold as their own,
	// which a business's money rules read (spec §9); Businesses what each
	// company declared, whose legal name its payouts name as the holder.
	Wallets    repositories.CustomerWalletRepository
	Businesses repositories.CustomerBusinessProfileRepository
	// Activation reconciles an approved business draft before its money is
	// refused as not active. Optional: without it a draft is refused.
	Activation BusinessActivation
	// BeneficiaryAccounts registers a bank beneficiary with the provider that
	// pays it out, for the off-ramp's bank rails. Only the off-ramp reads it.
	BeneficiaryAccounts BankAccountEnsurer
	// Beneficiaries reads a bank beneficiary's current revision and last
	// four digits, for a bank payout's create. Only the off-ramp reads it.
	Beneficiaries BankBeneficiaryReader
	// FundingAuthorizations holds the funding action a bank payout was
	// prepared with: the off-ramp's create writes it, and every order view
	// reads it.
	FundingAuthorizations repositories.OfframpFundingAuthorizationRepository
	// PixAccounts registers a Pix destination with a provider that holds it as
	// an object of its own. Only the off-ramp reads it.
	PixAccounts PixAccountEnsurer
}

// newEngine fills the unexported fields, so a service embedding an engine does
// not promote Deps' exported ones.
func newEngine(d Deps) engine {
	return engine{
		router: d.Router, config: d.Config, txRepo: d.Transactions,
		estimateRepo: d.Estimates, locker: d.Locker, audience: broadcast.NewAudience(d.Broadcaster, d.Members),
		notifier: d.Notifier, features: d.Features, pendingSteps: d.PendingSteps,
		vaRepo: d.VirtualAccounts, integRepo: d.Integrations, factory: d.Factory,
		laRepo: d.LiquidationAddresses, baRepo: d.BankAccounts,
		rules:   holderRules{wallets: d.Wallets, businesses: d.Businesses, activation: d.Activation},
		funding: d.FundingAuthorizations,
	}
}

// engine is the machinery both directions share.
type engine struct {
	router       *ramp.Router
	config       *ramp.Config
	txRepo       repositories.TransactionRepository
	estimateRepo repositories.RampEstimateRepository
	locker       locking.Locker
	audience     broadcast.Audience
	notifier     pushsvc.Notifier
	features     contracts.Feature
	pendingSteps PendingStepsSource
	vaRepo       repositories.CustomerVirtualAccountRepository
	integRepo    repositories.CustomerProviderIntegrationRepository
	factory      *ramp.Factory
	laRepo       repositories.UserLiquidationAddressRepository
	baRepo       repositories.UserBankAccountRepository
	rules        holderRules
	funding      repositories.OfframpFundingAuthorizationRepository
}

// requireRail refuses when an operator has switched the rail off, product-wide
// or for the user acting: rail flags are the operator's, whichever customer
// they act for (spec §5 F2).
//
// It is called where an operation is BORN — an estimate, a create — and
// nowhere on a read or on a refresh of an existing row. An order that already
// exists finishes even while its rail is closed: the veto stops new business,
// it does not strand what is in flight.
//
// A gate failure refuses. The error carries ErrRailDisabled so the client is
// told the rail is not serving, with the underlying cause left on the error for
// the log: during a flag-store outage a closed rail is the honest answer, and
// the alternative is a kill switch that stops working exactly when someone
// reaches for it.
func requireRail(ctx context.Context, features contracts.Feature, rail ramp.Rail, userID uint) error {
	on, err := features.IsActive(ctx, rail.Flag(), models.UserFlagScope(userID))
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("ramps: %s switch for user %d: %v (refusing)", rail, userID, err)
		return fmt.Errorf("%w: %v", ramp.ErrRailDisabled, err)
	}
	if !on {
		return ramp.ErrRailDisabled
	}
	return nil
}

// railPolicy loads the routing policy for a rail. A rail with no provider in
// its order is not open for business, and saying so beats defaulting to an
// unlimited one.
func railPolicy(config *ramp.Config, rail ramp.Rail) (ramp.RailPolicy, error) {
	policy, err := config.Rail(rail)
	if err != nil {
		return ramp.RailPolicy{}, fmt.Errorf("ramps: load rail settings: %w", err)
	}
	if len(policy.Providers) == 0 {
		return ramp.RailPolicy{}, fmt.Errorf("rail %q: %w", rail, ramp.ErrNoProviderAvailable)
	}
	return policy, nil
}

// checkLimits enforces the rail's configured bounds. They are expressed in BRL
// on both rails, because the PIX leg always settles in BRL and one unit
// removes the "minimum in which currency?" ambiguity.
func checkLimits(policy ramp.RailPolicy, amountBRL, token string) error {
	amount, ok := new(big.Rat).SetString(amountBRL)
	if !ok {
		return fmt.Errorf("ramps: amount %q is not a number", amountBRL)
	}
	if minBRL := policy.EffectiveMinBRL(token); minBRL != nil {
		floor, ok := new(big.Rat).SetString(*minBRL)
		if ok && amount.Cmp(floor) < 0 {
			return &ramp.LimitViolation{LimitBRL: *minBRL, Err: ramp.ErrAmountBelowMinimum}
		}
	}
	if policy.MaxBRL != nil {
		ceiling, ok := new(big.Rat).SetString(*policy.MaxBRL)
		if ok && amount.Cmp(ceiling) > 0 {
			return &ramp.LimitViolation{LimitBRL: *policy.MaxBRL, Err: ramp.ErrAmountAboveMaximum}
		}
	}
	return nil
}

// checkLimitsUSD enforces a bank rail's configured bounds, which are in USD
// of the source stablecoin (spec E0 item 5): amountUSD is what leaves the
// wallet. A violation carries LimitUSD, never LimitBRL.
func checkLimitsUSD(policy ramp.RailPolicy, amountUSD string) error {
	amount, ok := new(big.Rat).SetString(amountUSD)
	if !ok {
		return fmt.Errorf("ramps: amount %q is not a number", amountUSD)
	}
	if policy.MinUSD != nil {
		floor, ok := new(big.Rat).SetString(*policy.MinUSD)
		if ok && amount.Cmp(floor) < 0 {
			return &ramp.LimitViolation{LimitUSD: *policy.MinUSD, Err: ramp.ErrAmountBelowMinimum}
		}
	}
	if policy.MaxUSD != nil {
		ceiling, ok := new(big.Rat).SetString(*policy.MaxUSD)
		if ok && amount.Cmp(ceiling) > 0 {
			return &ramp.LimitViolation{LimitUSD: *policy.MaxUSD, Err: ramp.ErrAmountAboveMaximum}
		}
	}
	return nil
}

// quote prices a capability for the customer with the first provider on its
// rail that answers.
//
// It takes the whole capability, not the rail alone: a provider that cannot
// price the token the user asked for is not a candidate for it, and finding
// that out here is what lets the refusal name the provider that could.
//
// The estimate is kept only from a call that succeeded, so a provider that was
// failed over never leaves a partial answer behind. A money rule's refusal,
// raised by price before the provider is asked, is about the customer and is
// not failed over (failoverUnlessRefused).
func (e *engine) quote(
	ctx context.Context,
	customerID uint,
	capability ramp.Capability,
	price func(context.Context, ramp.ProviderAdapter, ramp.CustomerRef) (*ramp.Estimate, error),
) (*ramp.Estimate, ramp.ProviderCode, error) {
	return e.quoteWith(ctx, customerID, capability, failoverUnlessRefused, price)
}

// quoteWith is quote under an explicit failover policy. A bank payout quotes
// with noFailover: its destination is an account registered with one
// provider, so the next provider has nothing to pay out to.
func (e *engine) quoteWith(
	ctx context.Context,
	customerID uint,
	capability ramp.Capability,
	policy ramp.FailoverPolicy,
	price func(context.Context, ramp.ProviderAdapter, ramp.CustomerRef) (*ramp.Estimate, error),
) (*ramp.Estimate, ramp.ProviderCode, error) {
	var estimate *ramp.Estimate
	provider, err := e.router.ResolveWithCall(ctx, customerID, capability, policy,
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) error {
			result, err := price(ctx, adapter, ref)
			if err != nil {
				return err
			}
			estimate = result
			return nil
		})
	if err != nil {
		return nil, "", enrichKycRefusal(e.pendingSteps, customerID, err)
	}
	return estimate, provider, nil
}

// persistEstimate writes the quote of the customer, asked for by the user,
// and returns it as the client sees it. The external id is a UUID because it
// is the value that travels to the client and back; the primary key never
// leaves the server.
func (e *engine) persistEstimate(
	customerID, userID uint,
	provider ramp.ProviderCode,
	rail ramp.Rail,
	estimate *ramp.Estimate,
	snapshot estimateSnapshot,
) (*EstimateView, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("ramps: encode estimate snapshot: %w", err)
	}

	row := &models.RampEstimate{
		ExternalID:  uuid.NewString(),
		CustomerID:  customerID,
		UserID:      userID,
		Provider:    provider,
		Rail:        rail.String(),
		Binding:     estimate.Binding,
		ExpiresAt:   estimate.ExpiresAt,
		FeePercent:  snapshot.FeePercent,
		RawRequest:  "{}",
		RawResponse: string(encoded),
	}
	if estimate.ProviderQuoteID != "" {
		row.ProviderQuoteID = &estimate.ProviderQuoteID
	}
	if err := e.estimateRepo.Create(row); err != nil {
		return nil, err
	}
	return &EstimateView{
		EstimateID:               row.ExternalID,
		Rail:                     row.Rail,
		Rate:                     snapshot.Rate,
		FeePercent:               snapshot.FeePercent,
		FeeAmount:                snapshot.FeeAmount,
		FeeCurrency:              snapshot.FeeCurrency,
		SourceAmount:             snapshot.SourceAmount,
		EstimatedReceive:         snapshot.EstimatedReceive,
		ProviderDeductedAmount:   snapshot.ProviderDeductedAmount,
		ProviderDeductedCurrency: snapshot.ProviderDeductedCurrency,
		ProviderFeePercent:       snapshot.ProviderFeePercent,
		ProviderFeeAmount:        snapshot.ProviderFeeAmount,
		Binding:                  row.Binding,
		ExpiresAt:                row.ExpiresAt,
	}, nil
}

// loadEstimate resolves the customer's quote a create names, refusing one that
// can no longer price an order.
func (e *engine) loadEstimate(customerID uint, externalID, rail string) (*models.RampEstimate, *estimateSnapshot, error) {
	row, snapshot, err := e.loadEstimateRow(customerID, externalID, rail)
	if err != nil {
		return nil, nil, err
	}
	if row.IsConsumed() {
		return nil, nil, ramp.ErrEstimateConsumed
	}
	if row.IsExpired() {
		return nil, nil, ramp.ErrEstimateExpired
	}
	return row, snapshot, nil
}

// loadEstimateRow is loadEstimate without the consumed and expired checks:
// the quote of the customer's, whatever became of it, and what it priced.
func (e *engine) loadEstimateRow(customerID uint, externalID, rail string) (*models.RampEstimate, *estimateSnapshot, error) {
	row, err := e.estimateRepo.FindByCustomerAndExternalID(customerID, externalID)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, ErrEstimateNotFound
	}
	// A quote for the other direction is not a quote for this one, and treating
	// it as missing avoids leaking that it exists.
	if row.Rail != rail {
		return nil, nil, ErrEstimateNotFound
	}

	var snapshot estimateSnapshot
	if err := json.Unmarshal([]byte(row.RawResponse), &snapshot); err != nil {
		return nil, nil, fmt.Errorf("ramps: decode estimate snapshot: %w", err)
	}
	return row, &snapshot, nil
}

// dedupByEstimate returns an order already placed against this estimate, so a
// retry while the first create is in flight gets that order; once it finished,
// loadEstimate answers estimate_consumed first.
func (e *engine) dedupByEstimate(customerID uint, estimate *models.RampEstimate, txType string) (*models.Transaction, error) {
	existing, err := e.txRepo.FindByCustomerAndEstimate(customerID, estimate.Provider, estimate.ID, txType)
	if err != nil {
		return nil, fmt.Errorf("ramps: lookup dedup: %w", err)
	}
	return existing, nil
}

// reservationWindow bounds how long a row written before the upstream call may
// sit unanswered.
//
// It is not the order's deposit window — the provider's answer replaces it the
// moment one arrives. It is the horizon after which a create that never came
// back is presumed dead, so the reconciler expires the row instead of skipping
// it on every sweep forever. Comfortably longer than an adapter's own request
// timeout (30s for Bridge), because a slow answer is still an answer.
const reservationWindow = 2 * time.Minute

// reserve writes the row that holds the estimate while the provider is called.
//
// The row is written first, with its external id and derived idempotency key,
// so a create that times out leaves a record: the retry sends the provider the
// same key, and reconciliation has a row to attach the answer to. A row written
// after the call would be lost in exactly the case it matters.
func (e *engine) reserve(tx *models.Transaction) error {
	deadline := time.Now().Add(reservationWindow)
	tx.ExpiresAt = &deadline
	return e.txRepo.Create(tx)
}

// stale is a reservation past its deadline that never recorded the provider's
// order: the request that held it is gone (it died, or its last write failed).
func stale(tx *models.Transaction) bool {
	return tx.GatewayOrderID == "" && tx.ExpiresAt != nil && tx.ExpiresAt.Before(time.Now())
}

// resume takes a stale reservation back for a retry. Keeping the row keeps its
// idempotency key, so the provider replays an order it already holds instead
// of placing a second.
func (e *engine) resume(tx *models.Transaction) error {
	deadline := time.Now().Add(reservationWindow)
	tx.Status = models.TxStatusPending
	tx.ExpiresAt = &deadline
	if err := e.txRepo.Update(tx); err != nil {
		return fmt.Errorf("ramps: resume reservation: %w", err)
	}
	return nil
}

// releaseReservation frees a reservation whose upstream call provably never
// happened, so the user can redeem the same quote again.
//
// Only ErrRequestNotSent qualifies. Anything else — a timeout, a reset
// mid-response, a 5xx — is unknown state: the provider may be holding the
// order, and letting a retry place a second one is how one payment becomes two.
// Those rows keep the estimate, and a retry past the reservation deadline
// resumes them under the same key (see resume).
//
// Releasing means dropping the estimate link, not only the status: the unique
// on (customer_id, provider, estimate_id) is what actually holds the quote, and a
// retry that left it in place would collide on the insert.
func (e *engine) releaseReservation(tx *models.Transaction, cause error) {
	if !errors.Is(cause, ramp.ErrRequestNotSent) {
		return
	}
	// A provider that collects through a standing account refuses the create
	// before building a request, and says so with ErrNoProviderAvailable. That
	// is a negotiation, not an outage: the client asks for the account instead
	// and that deposit is the one that settles. Recording it as a failed order
	// leaves one dead row per successful deposit — half of production's on-ramp
	// rows, each blaming a provider that answered instantly and correctly.
	if errors.Is(cause, ramp.ErrNoProviderAvailable) {
		if err := e.txRepo.DiscardReservation(tx); err != nil {
			facades.Log().Errorf("ramps: discard reservation %s: %v", tx.ExternalID, err)
		}
		return
	}
	e.failReservation(tx, "provider unreachable: request never sent")
}

// failReservation fails a reservation and drops its estimate link, so the
// quote can be redeemed again (see releaseReservation for why the link).
func (e *engine) failReservation(tx *models.Transaction, reason string) {
	tx.Status = models.TxStatusFailed
	tx.FailureReason = &reason
	tx.EstimateID = nil
	tx.ExpiresAt = nil
	if err := e.txRepo.Update(tx); err != nil {
		// The row stays pending and the reconciler expires it at the deadline.
		// The user loses this quote and has to re-estimate — worse than the
		// happy path, better than a wrong write.
		facades.Log().Errorf("ramps: release reservation %s: %v", tx.ExternalID, err)
	}
}

// applyCreatedOrder merges a freshly created order onto its reservation row.
//
// Unlike a refresh, this replaces ExpiresAt outright: the row carried a short
// reservation deadline only because the deposit window was not known yet, and
// the provider's answer governs from here — including when that answer is "no
// window at all".
func applyCreatedOrder(tx *models.Transaction, order *ramp.RampOrder) {
	rampsvc.ApplyOrder(tx, order)
	tx.ExpiresAt = order.ExpiresAt
}

// enforceMfaStepUp mirrors the payout rule: a user with MFA on must present a
// code for an operation that moves money out. user is the account the
// controller already resolved, so the check costs no second read.
//
// A person is asked for it first, before any other gate, as they always were;
// a business after its local gates and money rules (stepUpFirst), so a wrong
// key never spends an MFA attempt (API contract §10).
func enforceMfaStepUp(ctx context.Context, validator MfaStepUpValidator, user identity.Subject, code, codeType string) error {
	enrolled, err := validator.Enrolled(user)
	if err != nil {
		return fmt.Errorf("ramps: mfa status: %w", err)
	}
	if !enrolled {
		return nil
	}
	if code == "" {
		return ramp.ErrMfaStepUpRequired
	}
	return validator.ValidateStepUpCode(ctx, user, code, codeType)
}

// stepUpFirst reports whether the customer's step-up comes before every other
// gate: a person's does.
func stepUpFirst(customer *models.Customer) bool { return !customer.IsBusiness() }

// newTransaction builds the row the create reserves, the customer's order
// placed by the user — see engine.reserve for why it is persisted before the
// upstream call.
func newTransaction(customerID, userID uint, provider ramp.ProviderCode, txType string, rail ramp.Rail) *models.Transaction {
	externalID := uuid.NewString()
	// Derived, not random: the same order must present the same key on every
	// attempt, and the external id is the one value that identifies it.
	idempotencyKey := txType + ":" + externalID
	railValue := rail.String()
	return &models.Transaction{
		CustomerID:     customerID,
		UserID:         userID,
		Provider:       provider,
		Type:           txType,
		ExternalID:     externalID,
		Status:         models.TxStatusPending,
		Rail:           &railValue,
		IdempotencyKey: &idempotencyKey,
		// The jsonb column will not take an empty string. The row is written
		// before the provider answers, so there is nothing to put in it yet.
		RawPayload: "{}",
	}
}

// publish pushes the order's status to the live subscribers of every user who
// acts for its customer.
func (e *engine) publish(tx *models.Transaction) {
	e.audience.Transaction(tx)
}

// toOrderView projects a transaction for the client.
func (e *engine) toOrderView(tx *models.Transaction) *OrderView {
	quotes := map[uint]models.RampEstimate{}
	if tx.EstimateID != nil {
		row, err := e.estimateRepo.FindByCustomerAndID(tx.CustomerID, *tx.EstimateID)
		if err != nil {
			facades.Log().Warningf("ramps: load estimate %d for customer %d: %v", *tx.EstimateID, tx.CustomerID, err)
		} else if row != nil {
			quotes[*tx.EstimateID] = *row
		}
	}
	view := e.projectOrderView(tx, quotes)
	return &view
}

func (e *engine) toOrderViews(rows []models.Transaction) []OrderView {
	if len(rows) == 0 {
		return nil
	}
	quotes := map[uint]models.RampEstimate{}
	if ids := collectEstimateIDs(rows); len(ids) > 0 {
		loaded, err := e.estimateRepo.FindByCustomerAndIDs(rows[0].CustomerID, ids)
		if err != nil {
			facades.Log().Warningf("ramps: batch load estimates for customer %d: %v", rows[0].CustomerID, err)
		} else {
			quotes = loaded
		}
	}
	views := make([]OrderView, 0, len(rows))
	for i := range rows {
		views = append(views, e.projectOrderView(&rows[i], quotes))
	}
	return views
}

func (e *engine) projectOrderView(tx *models.Transaction, quotes map[uint]models.RampEstimate) OrderView {
	view := OrderView{
		ID:          tx.ExternalID,
		Type:        tx.Type,
		Status:      tx.Status,
		AmountBRL:   tx.LegacyAmountBRL(),
		AmountToken: tx.AmountToken,
		Token:       tx.TargetToken,
		Rail:        deref(tx.Rail),
		// The fee columns a settled order carries: what XIP charged, and what
		// the provider's own conversion cost.
		FeePercent:        deref(tx.FeePercent),
		FeeAmount:         deref(tx.FeeAmount),
		FeeCurrency:       deref(tx.FeeCurrency),
		ExchangeRate:      deref(tx.ExchangeRate),
		ExchangeFeeAmount: deref(tx.ExchangeFeeAmount),
		ExpiresAt:         tx.ExpiresAt,
		CompletedAt:       tx.CompletedAt,
		FailureReason:     deref(tx.FailureReason),
	}
	if tx.CreatedAt != nil {
		created := tx.CreatedAt.StdTime()
		view.CreatedAt = &created
	}
	if view.ExchangeFeeAmount != "" {
		view.ExchangeFeeCurrency = exchangeFeeCurrency(tx)
	}
	e.applyDepositFields(&view, tx)
	applyEstimateFields(&view, tx, quotes)
	e.applyFiatFields(&view, tx)
	applyBankFields(&view, tx)
	e.applyFundingAction(&view, tx)
	maxDeliveryAttempts := ramp.DefaultOzavDeliveryMaxAttempts
	if tx.Type == models.TransactionTypeOnramp && e.config != nil {
		maxDeliveryAttempts = e.config.OzavDeliveryMaxAttempts()
	}
	view.OnrampPhase = DeriveOnrampPhase(tx, maxDeliveryAttempts)
	return view
}

// applyFiatFields fills the fiat side the USD rails added. A row written
// before fiat_currency existed is BRL.
//
// The bank account is read with the deleted ones: a payout outlives the
// account it paid, and its history still names it. A failed read leaves the
// account blank rather than failing an order that is otherwise correct; the
// account number is never part of the log line.
func (e *engine) applyFiatFields(view *OrderView, tx *models.Transaction) {
	view.FiatCurrency = tx.FiatCurrency
	if view.FiatCurrency == "" {
		view.FiatCurrency = models.FiatCurrencyBRL
	}
	view.AmountFiat = tx.AmountFiat
	view.PaymentRail = deref(tx.PaymentRail)
	if tx.BankAccountID == nil || e.baRepo == nil {
		return
	}
	account, err := e.baRepo.FindByIDWithTrashed(*tx.BankAccountID)
	if err != nil {
		facades.Log().Warningf("ramps: load bank account %d of user %d: %v", *tx.BankAccountID, tx.UserID, err)
		return
	}
	if account != nil && account.UserID == tx.UserID {
		view.BankAccountLabel = account.Label
		view.BankAccountLast4 = account.AccountLast4
	}
}

// applyBankFields fills a bank payout's fiat leg and masked destination. A
// Pix order leaves them empty, and the resource's omitempty drops them.
func applyBankFields(view *OrderView, tx *models.Transaction) {
	if !ramp.Rail(deref(tx.Rail)).IsBank() {
		return
	}
	view.BankRail = deref(tx.BankRail)
	view.BankAccountLast4 = deref(tx.BankAccountLast4)
	view.BankRoutingLast4 = deref(tx.BankRoutingLast4)
}

// applyFundingAction fills the action a bank payout awaiting its funds is to
// be signed with, while it is still valid (plan D9). Any failure to read it
// leaves the field empty: the rest of the order is still a correct answer,
// and the app asks again.
func (e *engine) applyFundingAction(view *OrderView, tx *models.Transaction) {
	if e.funding == nil || tx.Type != models.TransactionTypeOfframp ||
		tx.Status != models.TxStatusAwaitingFunds || !tx.FundedByAuthorization() {
		return
	}
	auth, err := e.funding.FindByTransaction(tx.ID)
	if err != nil {
		facades.Log().Warningf("ramps: load funding authorization of order %s: %v", tx.ExternalID, err)
		return
	}
	if auth == nil || auth.State != models.FundingAuthPrepared || !auth.ExpiresAt.After(time.Now()) {
		return
	}
	payload, err := openFundingPayload(auth.ActionPayloadCiphertext)
	if err != nil {
		facades.Log().Warningf("ramps: open funding action of order %s: %v", tx.ExternalID, err)
		return
	}
	view.FundingAction = &FundingActionView{
		Type: auth.ActionType, SubmissionMode: auth.SubmissionMode,
		ContentType: payload.ContentType, UnsignedTransaction: payload.UnsignedTransaction,
		ExpiresAt: auth.ExpiresAt, ActionRevision: auth.ActionRevision,
	}
}

// exchangeFeeCurrency is the unit a provider's receipt charges its conversion
// in: the source leg, which is the token on an off-ramp and reais on an
// on-ramp.
func exchangeFeeCurrency(tx *models.Transaction) string {
	if tx.Type == models.TransactionTypeOfframp {
		return strings.ToLower(tx.TargetToken)
	}
	if tx.FiatCurrency != "" {
		return tx.FiatCurrency
	}
	return brl
}

// deref reads a nullable column into the zero value the view carries for an
// absent one: "" for a string, so the resource's omitempty drops it.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// applyDepositFields fills the PIX/deposit side of the view: the instructions
// the user was shown to fund the order, the masked destination on an off-ramp,
// the settlement receipt, and — off-ramp only — which shape (deposit-first vs
// order-first) served it.
func (e *engine) applyDepositFields(view *OrderView, tx *models.Transaction) {
	if tx.DepositInstructions != nil {
		var instructions map[string]any
		if err := json.Unmarshal([]byte(*tx.DepositInstructions), &instructions); err == nil {
			view.DepositInstructions = instructions
		}
	}
	if tx.Type == models.TransactionTypeOfframp {
		// A provider whose settings cannot be read leaves the field empty; the
		// rest of the order is still a correct answer.
		if mode, err := e.depositMode(tx); err == nil {
			view.DepositMode = mode
		}
	}
	if tx.PixKey != nil {
		view.PixKey = pixkeys.Mask(*tx.PixKey, deref(tx.PixKeyType))
	}
	view.PixKeyType = deref(tx.PixKeyType)
	view.RecipientName = deref(tx.RecipientName)
	view.DepositTxSignature = deref(tx.DepositTxSignature)
}

// depositMode is the mode stored on the order, falling back to the provider's
// current one for a row written before that column was filled in.
func (e *engine) depositMode(tx *models.Transaction) (string, error) {
	if tx.DepositMode != nil {
		return *tx.DepositMode, nil
	}
	return DepositModeForProvider(e.config, tx.Provider)
}

// applyEstimateFields fills the quoted rate the order was created against, so
// the client can show what the user agreed to alongside what settled.
func applyEstimateFields(view *OrderView, tx *models.Transaction, quotes map[uint]models.RampEstimate) {
	if tx.EstimateID == nil {
		return
	}
	row, ok := quotes[*tx.EstimateID]
	if !ok {
		return
	}
	snap, ok := decodeEstimateSnapshot(row.RawResponse)
	if !ok || snap.Rate == "" {
		return
	}
	view.Rate = snap.Rate
	view.EstimatedReceive = snap.EstimatedReceive
	view.ProviderFeePercent = snap.ProviderFeePercent
	view.ProviderFeeAmount = snap.ProviderFeeAmount
}

// The PIX leg's fixed vocabulary. Both rails settle in BRL over PIX; the
// crypto side varies per order and comes from the request.
const (
	pixRail = "pix"
	brl     = "brl"
)

// createLockKey serializes concurrent creates for one customer. The estimate id
// is what identifies the create: both directions require one, and it is also
// what the database deduplicates on, with the customer whose quote it is.
func createLockKey(txType string, customerID uint, estimateID string) string {
	return fmt.Sprintf("%s:create:%d:%s", txType, customerID, estimateID)
}
