package ramps

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/jsonb"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrVirtualAccountNotFound is a read or an update for a customer that holds no
// standing account yet.
var ErrVirtualAccountNotFound = errors.New("ramps: virtual account not found")

// ErrProvisioningInFlight rejects a second provision while another request
// already holds the reservation and is talking to the provider. Hitchhiking
// would fire a parallel create — two deposit codes for one user. The client
// maps this to 409 and re-enters once the first call finishes (or the
// reservation is released).
var ErrProvisioningInFlight = errors.New("ramps: virtual account provisioning in flight")

// ClientSourceCurrency is the fiat the client API collects through a standing
// account when the request names none: every installed build asks for BRL
// without saying so. A request may name `usd` instead (Bridge USD spec §5.9).
const ClientSourceCurrency = brl

// VirtualAccountView is the standing account as the client sees it.
//
// `external_account_id` is absent for the reason EstimateView omits the
// provider: it is the provider's identifier, and which provider serves a rail
// is XIP's routing decision rather than part of the API contract.
type VirtualAccountView struct {
	Status         string
	SourceCurrency string
	Token          string
	Chain          string
	Wallet         string
	FeePercent     string
	// DepositInstructions is what the user pays into — for BRL/PIX the br_code
	// and the account holder name. It is permanent and carries no amount, so the
	// app shows it as a saved payment target rather than as a per-order QR.
	DepositInstructions map[string]any
}

// VirtualAccountService owns the customer's standing deposit account.
//
// It shares this package with the order services, for the reason ramps.go's
// header gives, but it does not embed `engine`. A standing account has no
// estimate to consume, no reservation to release and no order to broadcast, so
// most of the engine's collaborators would be nil while the embed still
// promoted the methods that use them. Holding only what it uses means a caller
// cannot reach a nil through a method that looks available.
type VirtualAccountService struct {
	router       *ramp.Router
	config       *ramp.Config
	locker       locking.Locker
	accounts     repositories.CustomerVirtualAccountRepository
	mfaStepUp    MfaStepUpValidator
	features     contracts.Feature
	pendingSteps PendingStepsSource
	rules        holderRules
}

// NewVirtualAccountService wires the standing-account service from the same
// Deps as the order services, keeping only what it uses.
func NewVirtualAccountService(d Deps, mfaStepUp MfaStepUpValidator) *VirtualAccountService {
	return &VirtualAccountService{
		router:       d.Router,
		config:       d.Config,
		locker:       d.Locker,
		accounts:     d.VirtualAccounts,
		mfaStepUp:    mfaStepUp,
		features:     d.Features,
		pendingSteps: d.PendingSteps,
		rules:        holderRules{wallets: d.Wallets, businesses: d.Businesses, activation: d.Activation},
	}
}

// Provision returns the customer's standing account for the currency, creating
// one, provisioned by user, only when it has none. The bool reports whether
// this call created it.
//
// The account is sticky across providers, and that is the point. Failing an
// order over to another provider is invisible to the user; failing a standing
// account over hands them a *new deposit code* for one they already saved in
// their bank app. So an existing account is returned whatever the rail order
// says today — the rail order decides where new accounts are provisioned and
// never touches an existing one. (Spec §11 refuses to rotate a deposit code for
// a fee change; this is the same rule.)
//
// Four layers of idempotency, in this order: the stickiness read, the re-read
// under the lock, the adapter's own read-then-create against the provider, and
// the partial unique on (customer_id, provider, source_currency). The last
// turns a bug into a failed insert instead of two deposit codes.
func (s *VirtualAccountService) Provision(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	in dto.ProvisionVirtualAccountDTO,
) (*VirtualAccountView, bool, error) {
	currency := normalizeSourceCurrency(in.SourceCurrency)

	existing, err := s.accounts.FindActiveByCustomerAndCurrency(customer.ID, currency)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return toVirtualAccountView(existing), false, nil
	}

	rail, err := railFor(currency)
	if err != nil {
		return nil, false, err
	}
	// The gate sits after the read, not before it. Provision is read-then-create
	// and only the create half is a birth: an account the user already holds is
	// theirs to see, and hiding it while the rail is closed would strand the
	// deposit code they saved in their bank app rather than stop new business.
	if err := requireRail(ctx, s.features, rail, user.ID); err != nil {
		return nil, false, err
	}
	policy, err := railPolicy(s.config, rail)
	if err != nil {
		return nil, false, err
	}

	var (
		account *models.CustomerVirtualAccount
		created bool
	)
	// Two-phase provision, same shape as offramp Create: the advisory lock covers
	// only "re-read, reserve the row", the provider call runs after the Postgres
	// transaction has committed, and a second lock pass finalizes. #15 marked
	// holding a transaction open across an upstream request as a blocker; this
	// path accepts the same trade-off the old comment named — Bridge does not
	// deduplicate virtual accounts, so read-then-create must stay serialized —
	// but confines the open transaction to short DB work instead of the
	// client's whole 30s timeout. A failed reserve leaves no active row; a
	// failed provider call leaves a provisioning row that blocks a second
	// create (ErrProvisioningInFlight → 409), like a pending order reservation.
	// The caller that reserved is the only one that talks to the provider.
	err = s.locker.WithLock(ctx, ProvisionLockKey(customer.ID), func(ctx context.Context) error {
		var reserveErr error
		account, created, reserveErr = s.reserveProvisioningUnderLock(ctx, user, customer, rail, currency, in, policy)
		return reserveErr
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		return toVirtualAccountView(account), false, nil
	}

	adapter, ref, err := s.router.ResolveProvider(ctx, customer.ID, rail, account.Provider)
	if err != nil {
		return nil, false, err
	}
	standing, err := ramp.StandingAccount(adapter)
	if err != nil {
		return nil, false, unservedRail(rail, err)
	}

	fee, err := policy.FeeFor(account.Provider)
	if err != nil {
		s.releaseProvisioningReservation(account, err)
		return nil, false, unservedRail(rail, err)
	}

	provisioned, err := standing.ProvisionVirtualAccount(ctx, ref, ramp.VirtualAccountRequest{
		SourceCurrency: currency,
		Destination: ramp.CryptoLeg{
			Currency: in.Token, PaymentRail: in.Chain, Address: in.Wallet,
		},
		FeePercent:     fee,
		IdempotencyKey: provisionIdempotencyKey(account),
	})
	if err != nil {
		s.releaseProvisioningReservation(account, err)
		return nil, false, unservedRail(rail, err)
	}

	err = s.locker.WithLock(ctx, ProvisionLockKey(customer.ID), func(ctx context.Context) error {
		var finalizeErr error
		account, created, finalizeErr = s.finalizeProvisioningUnderLock(customer.ID, currency, in, account, provisioned)
		return finalizeErr
	})
	if err != nil {
		return nil, false, err
	}
	return toVirtualAccountView(account), created, nil
}

// reserveProvisioningUnderLock is the first locked pass: re-read, refuse an
// in-flight reservation, or write one. It must not call the provider.
func (s *VirtualAccountService) reserveProvisioningUnderLock(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	rail ramp.Rail,
	currency string,
	in dto.ProvisionVirtualAccountDTO,
	policy ramp.RailPolicy,
) (*models.CustomerVirtualAccount, bool, error) {
	found, err := s.accounts.FindActiveByCustomerAndCurrency(customer.ID, currency)
	if err != nil {
		return nil, false, err
	}
	if found != nil {
		return found, false, nil
	}

	inFlight, err := s.accounts.FindProvisioningByCustomerAndCurrency(customer.ID, currency)
	if err != nil {
		return nil, false, err
	}
	if inFlight != nil {
		if inFlight.CreatedAt == nil || time.Since(inFlight.CreatedAt.StdTime()) < reservationWindow {
			// Someone is already talking to the provider. Hitchhiking would fire a
			// second create — the shape offramp_service avoids by returning the
			// found reservation as existing without ever calling CreateOfframp.
			return inFlight, false, ErrProvisioningInFlight
		}
		// Past the window the call it guarded is dead; the create is idempotent, so replace it.
		if err := s.accounts.Delete(inFlight); err != nil {
			return nil, false, fmt.Errorf("ramps: drop stale virtual account reservation: %w", err)
		}
	}

	// The chain is deliberately absent. A standing account collects BRL against
	// a permanent deposit code and picks the network when it delivers, not when
	// it is provisioned — pinning one here would refuse a provider that would
	// have served the deposit perfectly well.
	capability := ramp.Capability{Rail: rail, Token: in.Token}
	provider, err := s.router.ResolveWithCall(ctx, customer.ID, capability, ramp.FailoverOnUnsupported,
		func(ctx context.Context, adapter ramp.ProviderAdapter, _ ramp.CustomerRef) error {
			_, err := ramp.StandingAccount(adapter)
			return err
		})
	if err != nil {
		return nil, false, unservedRail(rail, enrichKycRefusal(s.pendingSteps, customer.ID, err))
	}
	// A business's money rules, after the router's gates and before the
	// reservation (API contract §10).
	if err := s.rules.onramp(customer, in.Chain, in.Wallet); err != nil {
		return nil, false, err
	}

	fee, err := policy.FeeFor(provider)
	if err != nil {
		return nil, false, unservedRail(rail, err)
	}

	reservation := newProvisioningReservation(customer.ID, user.ID, provider, currency, in, fee)
	if err := s.accounts.Create(reservation); err != nil {
		return nil, false, fmt.Errorf("ramps: persist virtual account reservation: %w", err)
	}
	return reservation, true, nil
}

// finalizeProvisioningUnderLock merges a confirmed provider account onto the
// reservation row, or returns an active account another request finalized first.
func (s *VirtualAccountService) finalizeProvisioningUnderLock(
	customerID uint,
	currency string,
	in dto.ProvisionVirtualAccountDTO,
	reservation *models.CustomerVirtualAccount,
	provisioned *ramp.VirtualAccount,
) (*models.CustomerVirtualAccount, bool, error) {
	found, err := s.accounts.FindActiveByCustomerAndCurrency(customerID, currency)
	if err != nil {
		return nil, false, err
	}
	if found != nil {
		return found, false, nil
	}

	current, err := s.accounts.FindByID(reservation.ID)
	if err != nil {
		return nil, false, err
	}
	if current == nil || current.Status != models.VirtualAccountStatusProvisioning {
		return nil, false, fmt.Errorf("ramps: provisioning reservation %d is no longer in flight", reservation.ID)
	}

	applyVirtualAccount(current, in, provisioned)
	current.ExternalAccountID = provisioned.ProviderAccountID
	current.Status = models.VirtualAccountStatusActive
	if err := s.accounts.Update(current); err != nil {
		return nil, false, fmt.Errorf("ramps: persist virtual account: %w", err)
	}
	return current, true, nil
}

// releaseProvisioningReservation drops a reservation whose upstream call provably
// never happened, so the customer can try again.
func (s *VirtualAccountService) releaseProvisioningReservation(row *models.CustomerVirtualAccount, cause error) {
	if row == nil || !errors.Is(cause, ramp.ErrRequestNotSent) {
		return
	}
	if err := s.accounts.Delete(row); err != nil {
		facades.Log().Errorf("ramps: release virtual account reservation %d: %v", row.ID, err)
	}
}

// Show returns the customer's standing account for the currency.
//
// It does not refresh against the provider, unlike an order's Show. An order
// moves through states the webhook stream can drop; an account sits still, and
// `ramps:sync-virtual-accounts` is what reconciles it.
func (s *VirtualAccountService) Show(customerID uint, sourceCurrency string) (*VirtualAccountView, error) {
	account, err := s.accounts.FindActiveByCustomerAndCurrency(customerID, normalizeSourceCurrency(sourceCurrency))
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrVirtualAccountNotFound
	}
	return toVirtualAccountView(account), nil
}

// UpdateDestination changes where the customer's account delivers its
// deposits, for user.
//
// It goes to the account's own provider, never to the head of the rail order:
// this is an edit of an object that already exists upstream. The deposit
// instructions survive the change (verified against the sandbox), so there is
// no code rotation to warn about — but the response is persisted rather than
// assumed, because the provider is the one that decides what the account now is.
//
// A person's step-up comes first for the same reason the off-ramp demands it
// before a payout: the br_code the user saved does not change, so a silent
// destination edit redirects every future deposit with no signal at the bank
// app. A business's comes after its gates and money rules (API contract §10).
func (s *VirtualAccountService) UpdateDestination(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	in dto.UpdateVirtualAccountDTO,
) (*VirtualAccountView, error) {
	if stepUpFirst(customer) {
		if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
			return nil, err
		}
	}
	currency := normalizeSourceCurrency(in.SourceCurrency)

	account, err := s.accounts.FindActiveByCustomerAndCurrency(customer.ID, currency)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrVirtualAccountNotFound
	}

	rail, err := railFor(account.SourceCurrency)
	if err != nil {
		return nil, err
	}
	adapter, ref, err := s.router.ResolveProvider(ctx, customer.ID, rail, account.Provider)
	if err != nil {
		return nil, err
	}
	standing, err := ramp.StandingAccount(adapter)
	if err != nil {
		return nil, unservedRail(rail, err)
	}
	if !stepUpFirst(customer) {
		if err := s.rules.onramp(customer, in.Chain, in.Wallet); err != nil {
			return nil, err
		}
		if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
			return nil, err
		}
	}

	destination := ramp.CryptoLeg{Currency: in.Token, PaymentRail: in.Chain, Address: in.Wallet}
	updated, err := standing.UpdateVirtualAccount(ctx, ref, account.ExternalAccountID,
		ramp.VirtualAccountUpdate{Destination: &destination})
	if err != nil {
		return nil, fmt.Errorf("ramps: update virtual account: %w", err)
	}

	applyVirtualAccount(account, in.ProvisionVirtualAccountDTO, updated)
	if err := s.accounts.Update(account); err != nil {
		return nil, fmt.Errorf("ramps: persist virtual account: %w", err)
	}
	return toVirtualAccountView(account), nil
}

// railFor maps a source currency onto the rail that collects it: BRL on
// pix_payin, USD on usd_payin (ramp.PayinRailFor). A currency with no rail is
// refused, never assumed onto a default.
func railFor(sourceCurrency string) (ramp.Rail, error) {
	rail, ok := ramp.PayinRailFor(sourceCurrency)
	if !ok {
		return "", fmt.Errorf("ramps: no rail collects %q: %w", sourceCurrency, ramp.ErrNoProviderAvailable)
	}
	return rail, nil
}

// unservedRail translates "nobody on this rail implements the port" into the
// client's vocabulary.
//
// ErrStandingAccountUnsupported is a routing signal: it says the provider was
// never asked to do anything. Once every candidate has answered it, the rail is
// simply not served this way, which is what ErrNoProviderAvailable already
// means to a client. Any other error is the provider's own and passes through
// untouched.
func unservedRail(rail ramp.Rail, err error) error {
	if errors.Is(err, ramp.ErrStandingAccountUnsupported) {
		return fmt.Errorf("rail %q collects through per-order instructions only: %w",
			rail, ramp.ErrNoProviderAvailable)
	}
	return err
}

// normalizeSourceCurrency puts the currency in the case the map and the column
// are keyed by. The client writes a currency the way a user reads one.
func normalizeSourceCurrency(currency string) string {
	return strings.ToLower(strings.TrimSpace(currency))
}

// ProvisionLockKey serializes one customer's provisioning across processes. It
// is per customer and not per currency: a customer provisioning two currencies
// at once is not a case worth admitting, and the wider key is the safer one.
// The admin unlink holds it too, so no reservation lands between its money
// check and its unlink, and so does the deletion of an account, for its
// individual customer. A person's key is the one it always was.
func ProvisionLockKey(customerID uint) string {
	return fmt.Sprintf("va:provision:%d", customerID)
}

// provisionIdempotencyKey is derived from the reservation, never random: a
// retry of a call whose response was lost must present the provider the same
// key, or the second attempt creates the second account this whole path exists
// to prevent.
//
// The reservation's id is part of it because a customer may provision again
// after its account is deactivated, and Bridge would answer a reused key with
// the first create's response — the deactivated account — for 24 hours, then
// refuse the key for good. Each reservation is one attempt at one account. It
// is keyed by the customer, so a person's keys are the ones already sent.
func provisionIdempotencyKey(reservation *models.CustomerVirtualAccount) string {
	return fmt.Sprintf("va:%d:%s:%d", reservation.CustomerID, reservation.SourceCurrency, reservation.ID)
}

// newProvisioningReservation builds the customer's row, provisioned by the
// user, held while the provider is called outside the advisory lock. The
// placeholder external id is unique per customer and currency and is replaced
// on finalize.
func newProvisioningReservation(
	customerID, userID uint,
	provider ramp.ProviderCode,
	sourceCurrency string,
	in dto.ProvisionVirtualAccountDTO,
	feePercent string,
) *models.CustomerVirtualAccount {
	row := &models.CustomerVirtualAccount{
		CustomerID:          customerID,
		UserID:              userID,
		Provider:            provider,
		ExternalAccountID:   provisionPlaceholderExternalID(customerID, sourceCurrency),
		SourceCurrency:      sourceCurrency,
		Status:              models.VirtualAccountStatusProvisioning,
		DestinationCurrency: strings.ToLower(in.Token),
		DestinationChain:    in.Chain,
		DestinationAddress:  in.Wallet,
		DepositInstructions: "{}",
		RawPayload:          "{}",
	}
	if feePercent != "" {
		row.FeePercent = &feePercent
	}
	return row
}

func provisionPlaceholderExternalID(customerID uint, sourceCurrency string) string {
	return fmt.Sprintf("provisioning:%d:%s", customerID, sourceCurrency)
}

// applyVirtualAccount merges the provider's view of an account onto the row.
func applyVirtualAccount(row *models.CustomerVirtualAccount, in dto.ProvisionVirtualAccountDTO, account *ramp.VirtualAccount) {
	if account.Status != "" {
		row.Status = account.Status
	}
	row.DestinationCurrency = cmp.Or(account.Destination.Currency, in.Token)
	row.DestinationChain = cmp.Or(account.Destination.PaymentRail, in.Chain)
	row.DestinationAddress = cmp.Or(account.Destination.Address, in.Wallet)
	if account.FeePercent != "" {
		row.FeePercent = &account.FeePercent
	}
	row.DepositInstructions = jsonb.Object(account.DepositInstructions)
	row.RawPayload = encodeRawJSON(account.Raw)
}

// toVirtualAccountView projects the row for the client.
func toVirtualAccountView(row *models.CustomerVirtualAccount) *VirtualAccountView {
	view := &VirtualAccountView{
		Status:         row.Status,
		SourceCurrency: row.SourceCurrency,
		// The token is rendered the way a user reads it, which is also the way the
		// client sends it; the provider leg is lowercase and stays that way below.
		Token:      strings.ToUpper(row.DestinationCurrency),
		Chain:      row.DestinationChain,
		Wallet:     row.DestinationAddress,
		FeePercent: deref(row.FeePercent),
	}
	var instructions map[string]any
	if err := json.Unmarshal([]byte(row.DepositInstructions), &instructions); err == nil {
		view.DepositInstructions = instructions
	}
	return view
}

// encodeRawJSON keeps the provider's untouched payload, falling back to an
// empty object for the same column constraint.
func encodeRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
