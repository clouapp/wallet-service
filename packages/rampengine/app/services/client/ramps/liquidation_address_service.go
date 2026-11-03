package ramps

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/usbank"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrPayoutRailUnsupportedForAccount is the provider saying the bank account
// does not take the rail asked: a wire to an account whose beneficiary address
// it did not accept. Nothing was created; the user picks the other rail or
// another account.
var ErrPayoutRailUnsupportedForAccount = errors.New("ramps: the bank account does not take this rail")

// ErrBusinessPayoutUnsupported refuses a USD payout address acting for a
// business. The bank account is a member's own, and a company's money rules
// (spec §9: its own wallet, its own account) have no USD counterpart yet: a
// company paying a member's personal account is exactly what they forbid on
// Pix. Refused until business payouts are designed.
var ErrBusinessPayoutUnsupported = errors.New("ramps: a business has no USD payout address")

// PayoutAddressView is the standing payout address as the client sees it.
// Like VirtualAccountView it never names the provider or its ids.
type PayoutAddressView struct {
	Rail          string
	BankAccountID uint
	Token         string
	Chain         string
	// Address is where the user sends the crypto.
	Address    string
	Reference  string
	FeePercent string
	// MinAmount is the rail's minimum in USD (min_amount_usd). Below it, after
	// XIP's fee, Bridge keeps the money as dust; Provision refuses such an
	// amount when the app names it before the send.
	MinAmount   string
	DepositMode string
	Status      string
}

// LiquidationAddressService owns the user's standing payout addresses. It
// holds only what it uses, for the reason VirtualAccountService gives.
type LiquidationAddressService struct {
	router       *ramp.Router
	config       *ramp.Config
	locker       locking.Locker
	features     contracts.Feature
	pendingSteps PendingStepsSource
	banks        repositories.UserBankAccountRepository
	destinations repositories.BankAccountDestinationRepository
	addresses    repositories.UserLiquidationAddressRepository
	// transactions, broadcaster and views serve SimulateDeposit only: it
	// records the drain a sandbox opened and shows it as an order.
	transactions repositories.TransactionRepository
	broadcaster  broadcast.Broadcaster
	views        engine
}

// NewLiquidationAddressService wires the service from the package Deps and the
// three repositories of a USD payout.
func NewLiquidationAddressService(
	d Deps,
	banks repositories.UserBankAccountRepository,
	destinations repositories.BankAccountDestinationRepository,
	addresses repositories.UserLiquidationAddressRepository,
) *LiquidationAddressService {
	return &LiquidationAddressService{
		router:       d.Router,
		config:       d.Config,
		locker:       d.Locker,
		features:     d.Features,
		pendingSteps: d.PendingSteps,
		banks:        banks,
		destinations: destinations,
		addresses:    addresses,
		transactions: d.Transactions,
		broadcaster:  d.Broadcaster,
		views:        newEngine(d),
	}
}

// Provision returns the address that pays the user's bank account over the
// rail from the token and chain asked, for the customer the request acts for,
// creating one only when the customer has none. The bool reports whether this
// call created it. The bank account is the user's, like a Pix key; the
// address and the account's registration are the customer's, held upstream
// on the customer's own.
//
// It mirrors VirtualAccountService.Provision: an address the customer holds
// is sticky (the rail order and the rail's switch decide where new ones are
// born, and never touch one in a wallet's address book), and a create is a
// two-phase reservation, with the provider called outside the advisory lock.
//
// An amount the app names before the send is held to the rail's minimum
// before the provider hears of anything: a refused amount leaves no address,
// no registered bank account and no rewritten reference behind.
func (s *LiquidationAddressService) Provision(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	in dto.ProvisionPayoutAddressDTO,
) (*PayoutAddressView, bool, error) {
	if customer.IsBusiness() {
		return nil, false, ErrBusinessPayoutUnsupported
	}
	rail := ramp.Rail(in.Rail)
	if !rail.IsUSDPayout() {
		return nil, false, fmt.Errorf("ramps: rail %q pays out through no liquidation address: %w",
			in.Rail, ramp.ErrNoProviderAvailable)
	}
	token := strings.ToLower(in.Token)

	bank, err := s.banks.FindOwned(user.ID, in.BankAccountID)
	if err != nil {
		return nil, false, err
	}
	if bank == nil {
		return nil, false, ErrBankAccountNotFound
	}

	existing, err := s.addresses.FindActiveRoute(customer.ID, bank.ID, rail.String(), in.Chain, token)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		// An amount means the app signs and sends next: a payout being born,
		// behind the gate a new address passes. Without one the address is
		// only shown, and a closed rail still shows it.
		if in.Amount != "" {
			if err := requireOpenRail(ctx, s.features, s.config, rail, user.ID); err != nil {
				return nil, false, err
			}
		}
		// The address keeps the fee it was born with.
		if err := s.checkAmount(rail, deref(existing.FeePercent), in.Amount); err != nil {
			return nil, false, err
		}
		synced, err := s.syncAddress(ctx, customer.ID, rail, existing, in.Reference, in.Wallet)
		if err != nil {
			return nil, false, err
		}
		return s.view(synced), false, nil
	}
	return s.create(ctx, user, customer, rail, token, bank, in)
}

// requireOpenRail is the gate of a payout's birth: the rail switched on for
// the user (rail_disabled) and with a provider in its order
// (no_provider_available).
func requireOpenRail(ctx context.Context, features contracts.Feature, config *ramp.Config, rail ramp.Rail, userID uint) error {
	if err := requireRail(ctx, features, rail, userID); err != nil {
		return err
	}
	_, err := railPolicy(config, rail)
	return err
}

// checkAmount is checkPayoutMinimum against the rail's floor, for a provision
// that named an amount. One that did not (the screen opening) refuses nothing.
func (s *LiquidationAddressService) checkAmount(rail ramp.Rail, feePercent, amount string) error {
	if amount == "" {
		return nil
	}
	policy, err := s.config.Rail(rail)
	if err != nil {
		return err
	}
	return checkPayoutMinimum(deref(policy.MinUSD), feePercent, amount)
}

// checkPayoutMinimum refuses a token amount whose payout, after XIP's fee,
// falls below the rail's min_amount_usd: Bridge keeps such a deposit as dust.
// USDC is taken at par with the dollar, as Bridge pays it. No minimum set
// refuses nothing.
func checkPayoutMinimum(minUSD, feePercent, amount string) error {
	if minUSD == "" {
		return nil
	}
	value, ok := new(big.Rat).SetString(amount)
	if !ok {
		return fmt.Errorf("ramps: amount %q is not a number", amount)
	}
	floor, ok := new(big.Rat).SetString(minUSD)
	if !ok {
		return nil
	}
	net := new(big.Rat).Set(value)
	if fee, ok := new(big.Rat).SetString(feePercent); ok && feePercent != "" {
		keep := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(fee, big.NewRat(100, 1)))
		net.Mul(net, keep)
	}
	if net.Cmp(floor) < 0 {
		return &ramp.LimitViolation{LimitUSD: minUSD, Err: ramp.ErrAmountBelowMinimum}
	}
	return nil
}

// create provisions a new address for the route: the rail's gate, the
// reservation, the provider, and the finalize.
func (s *LiquidationAddressService) create(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	rail ramp.Rail,
	token string,
	bank *repositories.UserBankAccountView,
	in dto.ProvisionPayoutAddressDTO,
) (*PayoutAddressView, bool, error) {
	// The gate sits after the read, as on the virtual account: only the
	// create is a birth.
	if err := requireRail(ctx, s.features, rail, user.ID); err != nil {
		return nil, false, err
	}
	policy, err := railPolicy(s.config, rail)
	if err != nil {
		return nil, false, err
	}

	var (
		reservation *models.UserLiquidationAddress
		created     bool
	)
	err = s.locker.WithLock(ctx, PayoutAddressLockKey(customer.ID), func(ctx context.Context) error {
		var reserveErr error
		reservation, created, reserveErr = s.reserveUnderLock(ctx, user.ID, customer.ID, rail, token, bank.ID, in, policy)
		return reserveErr
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		return s.view(reservation), false, nil
	}

	provisioned, err := s.provisionUpstream(ctx, customer.ID, rail, token, bank, in, reservation)
	if err != nil {
		s.releaseReservation(reservation)
		return nil, false, err
	}

	var address *models.UserLiquidationAddress
	err = s.locker.WithLock(ctx, PayoutAddressLockKey(customer.ID), func(ctx context.Context) error {
		var finalizeErr error
		address, created, finalizeErr = s.finalizeUnderLock(user.ID, customer.ID, rail, token, bank.ID, in, reservation, provisioned)
		return finalizeErr
	})
	if errors.Is(err, ErrBankAccountNotFound) {
		// The account was deleted while the provider was creating the address.
		// The delete told the provider to stop paying the registrations it
		// found; this one may have been recorded after, so tell it again.
		s.DeactivateBankAccountUpstream(ctx, bank.ID)
	}
	if err != nil {
		return nil, false, err
	}
	return s.view(address), created, nil
}

// SimulateDeposit asks the sandbox to pretend the user sent Amount of the
// token to the customer's address, then records the drain it opened the way
// the webhook would: the sandbox delivers no webhook to a local server, so
// without this a sandbox payout would never appear.
func (s *LiquidationAddressService) SimulateDeposit(
	ctx context.Context,
	user *models.User,
	customer *models.Customer,
	in dto.SimulatePayoutDepositDTO,
) (*OrderView, error) {
	if customer.IsBusiness() {
		return nil, ErrBusinessPayoutUnsupported
	}
	rail := ramp.Rail(in.Rail)
	token := strings.ToLower(in.Token)

	// The account is the user's: a route is the customer's, and a member
	// must not reach another's account through it.
	bank, err := s.banks.FindOwned(user.ID, in.BankAccountID)
	if err != nil {
		return nil, err
	}
	if bank == nil {
		return nil, ErrBankAccountNotFound
	}

	address, err := s.addresses.FindActiveRoute(customer.ID, bank.ID, rail.String(), in.Chain, token)
	if err != nil {
		return nil, err
	}
	if address == nil {
		return nil, ErrOrderNotFound
	}
	mode, err := DepositModeForProvider(s.config, address.Provider)
	if err != nil {
		return nil, err
	}
	if mode != DepositModeSimulated {
		return nil, ramp.ErrSandboxOnly
	}
	// The sandbox refuses below the minimum as the app's send does.
	shown := s.view(address)
	if err := checkPayoutMinimum(shown.MinAmount, shown.FeePercent, in.Amount); err != nil {
		return nil, err
	}
	// A simulated deposit opens a payout: a birth, so the rail must be open.
	if err := requireOpenRail(ctx, s.features, s.config, rail, user.ID); err != nil {
		return nil, err
	}

	adapter, ref, err := s.router.ResolveProvider(ctx, customer.ID, rail, address.Provider)
	if err != nil {
		return nil, err
	}
	payout, err := ramp.PayoutAddress(adapter)
	if err != nil {
		return nil, unservedPayoutRail(rail, err)
	}
	drainID, err := payout.SimulateLiquidationDeposit(ctx, ref, address.ExternalAddressID, ramp.SimulatedDeposit{
		Currency: token, Amount: in.Amount, IdempotencyKey: "la-sim:" + uuid.NewString(),
	})
	if err != nil {
		return nil, fmt.Errorf("ramps: simulate payout deposit: %w", err)
	}
	drain, err := payout.GetDrain(ctx, ref, address.ExternalAddressID, drainID)
	if err != nil {
		return nil, fmt.Errorf("ramps: read simulated drain: %w", err)
	}

	tx := &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, address, drain)
	err = s.transactions.Create(tx)
	if errors.Is(err, repositories.ErrDuplicateDeposit) {
		// The sandbox's webhook recorded the drain first: it is the order.
		recorded, findErr := s.transactions.FindByProviderAndDepositID(address.Provider, drainID)
		if findErr != nil {
			return nil, fmt.Errorf("ramps: read recorded simulated drain: %w", findErr)
		}
		if recorded == nil {
			return nil, fmt.Errorf("ramps: simulated drain %s vanished after a duplicate", drainID)
		}
		view := s.views.projectOrderView(recorded, nil)
		return &view, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ramps: persist simulated drain: %w", err)
	}
	s.broadcaster.Publish(tx.UserID, broadcast.NewTransactionEvent(tx))
	view := s.views.projectOrderView(tx, nil)
	return &view, nil
}

// reserveUnderLock is the first locked pass: re-read, refuse an in-flight
// reservation, or write one. It must not call the provider.
func (s *LiquidationAddressService) reserveUnderLock(
	ctx context.Context,
	userID, customerID uint,
	rail ramp.Rail,
	token string,
	bankAccountID uint,
	in dto.ProvisionPayoutAddressDTO,
	policy ramp.RailPolicy,
) (*models.UserLiquidationAddress, bool, error) {
	found, err := s.addresses.FindActiveRoute(customerID, bankAccountID, rail.String(), in.Chain, token)
	if err != nil {
		return nil, false, err
	}
	if found != nil {
		// Another request finalized the route first: its fee is the one.
		if err := s.checkAmount(rail, deref(found.FeePercent), in.Amount); err != nil {
			return nil, false, err
		}
		return found, false, nil
	}
	// Under the lock BankAccountService.Delete takes: an account deleted
	// since the request read it gets no reservation.
	if err := s.requireBankAccount(userID, bankAccountID); err != nil {
		return nil, false, err
	}

	inFlight, err := s.addresses.FindProvisioningRoute(customerID, bankAccountID, rail.String(), in.Chain, token)
	if err != nil {
		return nil, false, err
	}
	if inFlight != nil {
		if inFlight.CreatedAt == nil || time.Since(inFlight.CreatedAt.StdTime()) < reservationWindow {
			return inFlight, false, ErrProvisioningInFlight
		}
		// Past the window the call it guarded is dead; the create is
		// idempotent, so replace it.
		if err := s.addresses.Delete(inFlight); err != nil {
			return nil, false, fmt.Errorf("ramps: drop stale payout address reservation: %w", err)
		}
	}

	capability := ramp.Capability{Rail: rail, Token: in.Token, Chain: in.Chain}
	provider, err := s.router.ResolveWithCall(ctx, customerID, capability, ramp.FailoverOnUnsupported,
		func(ctx context.Context, adapter ramp.ProviderAdapter, _ ramp.CustomerRef) error {
			_, err := ramp.PayoutAddress(adapter)
			return err
		})
	if err != nil {
		return nil, false, unservedPayoutRail(rail, enrichKycRefusal(s.pendingSteps, customerID, err))
	}
	// The fee of the provider that serves the address, fixed on it from here.
	fee, err := policy.FeeFor(provider)
	if err != nil {
		return nil, false, unservedPayoutRail(rail, err)
	}
	// Before the reservation, so a refused amount leaves no row and the
	// provider is never called.
	if err := s.checkAmount(rail, fee, in.Amount); err != nil {
		return nil, false, err
	}

	reservation := &models.UserLiquidationAddress{
		CustomerID:        customerID,
		UserID:            userID,
		Provider:          provider,
		UserBankAccountID: bankAccountID,
		Rail:              rail.String(),
		Chain:             in.Chain,
		Currency:          token,
		ExternalAddressID: payoutPlaceholderExternalID(customerID, bankAccountID, rail, in.Chain, token),
		ReturnAddress:     in.Wallet,
		Reference:         payoutOptional(deref(in.Reference)),
		FeePercent:        payoutOptional(fee),
		Status:            models.LiquidationAddressStatusProvisioning,
		RawPayload:        "{}",
	}
	err = s.addresses.Create(reservation)
	if errors.Is(err, repositories.ErrDuplicateLiquidationAddressRoute) {
		// Another process holds the route: the advisory lock is per process
		// group, the unique is the database's.
		return nil, false, ErrProvisioningInFlight
	}
	if err != nil {
		return nil, false, fmt.Errorf("ramps: persist payout address reservation: %w", err)
	}
	return reservation, true, nil
}

// requireBankAccount answers ErrBankAccountNotFound for an account the user no
// longer holds.
func (s *LiquidationAddressService) requireBankAccount(userID, bankAccountID uint) error {
	bank, err := s.banks.FindOwned(userID, bankAccountID)
	if err != nil {
		return err
	}
	if bank == nil {
		return ErrBankAccountNotFound
	}
	return nil
}

// provisionUpstream talks to the reservation's provider: the bank account's
// registration first (once per account and provider), then the address.
func (s *LiquidationAddressService) provisionUpstream(
	ctx context.Context,
	customerID uint,
	rail ramp.Rail,
	token string,
	bank *repositories.UserBankAccountView,
	in dto.ProvisionPayoutAddressDTO,
	reservation *models.UserLiquidationAddress,
) (*ramp.LiquidationAddress, error) {
	adapter, ref, err := s.router.ResolveProvider(ctx, customerID, rail, reservation.Provider)
	if err != nil {
		return nil, err
	}
	payout, err := ramp.PayoutAddress(adapter)
	if err != nil {
		return nil, unservedPayoutRail(rail, err)
	}

	externalAccountID, supported, err := s.destinationFor(ctx, customerID, reservation.Provider, payout, ref, bank)
	if err != nil {
		return nil, err
	}
	if !railAccepted(rail, supported) {
		return nil, ErrPayoutRailUnsupportedForAccount
	}

	return payout.ProvisionLiquidationAddress(ctx, ref, ramp.LiquidationAddressRequest{
		Rail:              rail,
		Source:            ramp.CryptoLeg{Currency: token, PaymentRail: in.Chain},
		ExternalAccountID: externalAccountID,
		ReturnAddress:     in.Wallet,
		Reference:         deref(in.Reference),
		FeePercent:        deref(reservation.FeePercent),
		// Keyed by reservation: a new reservation may carry another wallet or
		// reference, and a key reused with another body is refused or answered
		// with the old address. A retry within one reservation keeps the key.
		IdempotencyKey: fmt.Sprintf("la:%d:%d:%s:%s:%s:%d", customerID, bank.ID, rail, in.Chain, token, reservation.ID),
	})
}

// destinationFor returns the provider's id for the bank account on the
// customer and the rails it said the account takes, registering the account
// the first time the customer pays it.
func (s *LiquidationAddressService) destinationFor(
	ctx context.Context,
	customerID uint,
	provider string,
	payout ramp.LiquidationAddressAdapter,
	ref ramp.CustomerRef,
	bank *repositories.UserBankAccountView,
) (string, []string, error) {
	known, err := s.destinations.Find(customerID, bank.ID, provider)
	if err != nil {
		return "", nil, err
	}
	if known != nil {
		return decodeDestination(known)
	}

	// The account was validated with two words at least when it was saved, so
	// the split cannot fail here; an empty half would be Bridge's to refuse.
	first, last, _ := usbank.SplitOwnerName(bank.OwnerName)
	registered, err := payout.RegisterBankAccount(ctx, ref, ramp.BankAccountRegistration{
		OwnerName:      bank.OwnerName,
		OwnerFirstName: first,
		OwnerLastName:  last,
		BankName:       bank.BankName,
		AccountName:    bank.Label,
		RoutingNumber:  bank.RoutingNumber,
		AccountNumber:  bank.AccountNumber,
		AccountType:    bank.AccountType,
		Address:        bank.Address,
		IdempotencyKey: fmt.Sprintf("bank:%d:%d", customerID, bank.ID),
	})
	if err != nil {
		return "", nil, err
	}

	rails := "[]"
	if len(registered.PaymentRails) > 0 {
		encoded, err := json.Marshal(registered.PaymentRails)
		if err != nil {
			return "", nil, fmt.Errorf("ramps: encode payment rails: %w", err)
		}
		rails = string(encoded)
	}
	err = s.destinations.Create(&models.BankAccountDestination{
		CustomerID:        customerID,
		UserBankAccountID: bank.ID,
		Provider:          provider,
		DestinationRef:    registered.ExternalAccountID,
		PaymentRails:      rails,
	})
	if errors.Is(err, repositories.ErrDuplicateBankAccountDestination) {
		// A concurrent provision of another rail registered the same account
		// (same idempotency key, same provider id) and recorded it first.
		return s.recordedDestination(customerID, bank.ID, provider)
	}
	if err != nil {
		return "", nil, fmt.Errorf("ramps: persist bank account destination: %w", err)
	}
	return registered.ExternalAccountID, registered.PaymentRails, nil
}

// recordedDestination reads back the registration another request recorded.
func (s *LiquidationAddressService) recordedDestination(customerID, bankAccountID uint, provider string) (string, []string, error) {
	known, err := s.destinations.Find(customerID, bankAccountID, provider)
	if err != nil {
		return "", nil, err
	}
	if known == nil {
		return "", nil, fmt.Errorf("ramps: bank account %d destination vanished after a duplicate", bankAccountID)
	}
	return decodeDestination(known)
}

// decodeDestination is a recorded registration as destinationFor answers it.
func decodeDestination(known *models.BankAccountDestination) (string, []string, error) {
	var supported []string
	if err := json.Unmarshal([]byte(known.PaymentRails), &supported); err != nil {
		return "", nil, fmt.Errorf("ramps: decode payment rails of bank account %d: %w", known.UserBankAccountID, err)
	}
	return known.DestinationRef, supported, nil
}

// finalizeUnderLock merges the provider's address onto the reservation, or
// returns an active address another request finalized first.
//
// The bank account is read again first, under the lock its deletion takes:
// one deleted while the provider was creating the address answers
// ErrBankAccountNotFound, and the address is recorded inactive, so a drain
// that still reaches it is held for review instead of being an unknown
// address.
//
// An address Bridge answered that a row already holds (an earlier
// provision's, gone inactive) is that row's again: the reservation is dropped
// and the row takes the answer, since (provider, external id) is unique.
func (s *LiquidationAddressService) finalizeUnderLock(
	userID, customerID uint,
	rail ramp.Rail,
	token string,
	bankAccountID uint,
	in dto.ProvisionPayoutAddressDTO,
	reservation *models.UserLiquidationAddress,
	provisioned *ramp.LiquidationAddress,
) (*models.UserLiquidationAddress, bool, error) {
	found, err := s.addresses.FindActiveRoute(customerID, bankAccountID, rail.String(), in.Chain, token)
	if err != nil {
		return nil, false, err
	}
	if found != nil {
		return found, false, nil
	}

	current, err := s.addresses.FindByID(reservation.ID)
	if err != nil {
		return nil, false, err
	}
	gone := s.requireBankAccount(userID, bankAccountID)
	if errors.Is(gone, ErrBankAccountNotFound) {
		if current != nil {
			applyProvisionedAddress(current, provisioned)
			current.Status = models.LiquidationAddressStatusInactive
			if err := s.addresses.Update(current); err != nil {
				facades.Log().Errorf("ramps: record payout address %d of a deleted bank account: %v", current.ID, err)
			}
		}
		return nil, false, ErrBankAccountNotFound
	}
	if gone != nil {
		return nil, false, gone
	}
	if current == nil || current.Status != models.LiquidationAddressStatusProvisioning {
		return nil, false, fmt.Errorf("ramps: payout address reservation %d is no longer in flight", reservation.ID)
	}

	known, err := s.addresses.FindByProviderAndExternalID(current.Provider, provisioned.ProviderAddressID)
	if err != nil {
		return nil, false, err
	}
	if known != nil && known.ID != current.ID {
		// The reservation goes first: the row coming back to life holds the
		// route the reservation holds, and only one may.
		if err := s.addresses.Delete(current); err != nil {
			return nil, false, fmt.Errorf("ramps: drop payout address reservation: %w", err)
		}
		known.UserID, known.ReturnAddress = current.UserID, current.ReturnAddress
		current = known
	}

	applyProvisionedAddress(current, provisioned)
	if err := s.addresses.Update(current); err != nil {
		return nil, false, fmt.Errorf("ramps: persist payout address: %w", err)
	}
	return current, true, nil
}

// applyProvisionedAddress is the provider's answer on the row.
func applyProvisionedAddress(row *models.UserLiquidationAddress, provisioned *ramp.LiquidationAddress) {
	row.ExternalAddressID = provisioned.ProviderAddressID
	row.Address = provisioned.Address
	row.Status = provisioned.Status
	row.Reference = payoutOptional(provisioned.Reference)
	if provisioned.FeePercent != "" {
		fee := provisioned.FeePercent
		row.FeePercent = &fee
	}
	if provisioned.ReturnAddress != "" {
		row.ReturnAddress = provisioned.ReturnAddress
	}
	row.RawPayload = encodeRawJSON(provisioned.Raw)
}

// releaseReservation drops the reservation of a provision that failed, so the
// user can try again at once instead of waiting out the window. Whatever the
// failure, a retry cannot duplicate the address: the adapter reads the route
// before creating it, and Bridge refuses a second address for one route.
func (s *LiquidationAddressService) releaseReservation(row *models.UserLiquidationAddress) {
	if row == nil {
		return
	}
	if err := s.addresses.Delete(row); err != nil {
		facades.Log().Errorf("ramps: release payout address reservation %d: %v", row.ID, err)
	}
}

// syncAddress writes a changed reference or return wallet through to the
// address's own provider, in one update, and persists what the provider
// answered. A nil reference was not sent, which keeps the address's own; the
// wallet is always sent, and a different one is where a drain the provider
// cannot pay out goes back from now on (Bridge's return_instructions).
func (s *LiquidationAddressService) syncAddress(
	ctx context.Context,
	customerID uint,
	rail ramp.Rail,
	address *models.UserLiquidationAddress,
	reference *string,
	wallet string,
) (*models.UserLiquidationAddress, error) {
	var change ramp.LiquidationAddressUpdate
	if reference != nil && deref(address.Reference) != *reference {
		change.Reference = reference
	}
	if wallet != "" && wallet != address.ReturnAddress {
		change.ReturnAddress = wallet
	}
	if change.Reference == nil && change.ReturnAddress == "" {
		return address, nil
	}
	adapter, ref, err := s.router.ResolveProvider(ctx, customerID, rail, address.Provider)
	if err != nil {
		return nil, err
	}
	payout, err := ramp.PayoutAddress(adapter)
	if err != nil {
		return nil, unservedPayoutRail(rail, err)
	}
	updated, err := payout.UpdateLiquidationAddress(ctx, ref, address.ExternalAddressID, change)
	if err != nil {
		return nil, fmt.Errorf("ramps: update payout address: %w", err)
	}
	address.Reference = payoutOptional(updated.Reference)
	if change.ReturnAddress != "" {
		// The provider's answer when it says one, the wallet sent otherwise:
		// the update succeeded.
		address.ReturnAddress = cmp.Or(updated.ReturnAddress, change.ReturnAddress)
	}
	address.RawPayload = encodeRawJSON(updated.Raw)
	if err := s.addresses.Update(address); err != nil {
		return nil, fmt.Errorf("ramps: persist payout address: %w", err)
	}
	return address, nil
}

// DeactivateBankAccountAddresses marks every address of a deleted bank account
// inactive, whichever customer it paid for: none is offered again, and a drain
// that still reaches one is held for review by the webhook instead of opening
// a payout.
func (s *LiquidationAddressService) DeactivateBankAccountAddresses(userID, bankAccountID uint) error {
	return s.addresses.DeactivateByBankAccount(userID, bankAccountID)
}

// DeactivateBankAccountUpstream deactivates each registration of a deleted bank
// account at its provider, on the customer that holds it. Bridge has no
// deactivate for a liquidation address;
// a deactivated external account is what stops the addresses paying it. Best
// effort: the account is already deleted here, so a failure is logged (ids
// only) for an operator, and a drain that still arrives is held for review.
func (s *LiquidationAddressService) DeactivateBankAccountUpstream(ctx context.Context, bankAccountID uint) {
	registrations, err := s.destinations.ListByBankAccount(bankAccountID)
	if err != nil {
		facades.Log().Errorf("ramps: list registrations of deleted bank account %d: %v", bankAccountID, err)
		return
	}
	s.DeactivateRegistrationsUpstream(ctx, registrations)
}

// UserBankAccountRegistrations is every registration of the user's live bank
// accounts. An account deletion reads it before it removes the accounts, and
// hands it to DeactivateRegistrationsUpstream once the deletion holds.
func (s *LiquidationAddressService) UserBankAccountRegistrations(userID uint) ([]models.BankAccountDestination, error) {
	return s.destinations.ListByUser(userID)
}

// DeactivateRegistrationsUpstream deactivates each registration at its
// provider, best effort as DeactivateBankAccountUpstream: a failure is logged
// (ids only) and a drain that still arrives is held for review.
func (s *LiquidationAddressService) DeactivateRegistrationsUpstream(
	ctx context.Context,
	registrations []models.BankAccountDestination,
) {
	for _, registration := range registrations {
		if err := s.deactivateRegistration(ctx, registration); err != nil {
			facades.Log().Errorf("ramps: deactivate %s external account %s of deleted bank account %d: %v",
				registration.Provider, registration.DestinationRef, registration.UserBankAccountID, err)
		}
	}
}

func (s *LiquidationAddressService) deactivateRegistration(
	ctx context.Context,
	registration models.BankAccountDestination,
) error {
	// The registration serves both payout rails; either resolves the provider.
	adapter, ref, err := s.router.ResolveProvider(ctx, registration.CustomerID, ramp.RailUSDACHPayout, registration.Provider)
	if err != nil {
		return err
	}
	payout, err := ramp.PayoutAddress(adapter)
	if err != nil {
		return err
	}
	return payout.DeactivateBankAccount(ctx, ref, registration.DestinationRef)
}

// view projects the row for the client. The minimum and the deposit mode are
// read now, not stored: they are the rail's and the provider's today.
func (s *LiquidationAddressService) view(row *models.UserLiquidationAddress) *PayoutAddressView {
	view := &PayoutAddressView{
		Rail:          row.Rail,
		BankAccountID: row.UserBankAccountID,
		Token:         strings.ToUpper(row.Currency),
		Chain:         row.Chain,
		Address:       row.Address,
		Reference:     deref(row.Reference),
		FeePercent:    deref(row.FeePercent),
		Status:        row.Status,
	}
	if policy, err := s.config.Rail(ramp.Rail(row.Rail)); err == nil {
		view.MinAmount = deref(policy.MinUSD)
	}
	if mode, err := DepositModeForProvider(s.config, row.Provider); err == nil {
		view.DepositMode = mode
	}
	return view
}

// railAccepted reads Bridge's payment_rails.supported. An empty list is the
// provider saying nothing, which refuses nothing.
func railAccepted(rail ramp.Rail, supported []string) bool {
	if len(supported) == 0 {
		return true
	}
	if rail == ramp.RailUSDWirePayout {
		return slices.Contains(supported, "wire")
	}
	return slices.Contains(supported, "ach") || slices.Contains(supported, "ach_same_day")
}

// unservedPayoutRail is unservedRail for the payout port.
func unservedPayoutRail(rail ramp.Rail, err error) error {
	if errors.Is(err, ramp.ErrLiquidationAddressUnsupported) {
		return fmt.Errorf("rail %q pays out through no liquidation address: %w", rail, ramp.ErrNoProviderAvailable)
	}
	return err
}

// PayoutAddressLockKey serializes one customer's payout address provisioning
// across processes, per customer like ProvisionLockKey.
func PayoutAddressLockKey(customerID uint) string {
	return fmt.Sprintf("la:provision:%d", customerID)
}

// payoutPlaceholderExternalID is the reservation's external id: unique per
// route, replaced on finalize.
func payoutPlaceholderExternalID(customerID, bankAccountID uint, rail ramp.Rail, chain, token string) string {
	return fmt.Sprintf("provisioning:%d:%d:%s:%s:%s", customerID, bankAccountID, rail, chain, token)
}

// payoutOptional is nil for "", so a nullable column says "none" rather than
// storing an empty string.
func payoutOptional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
