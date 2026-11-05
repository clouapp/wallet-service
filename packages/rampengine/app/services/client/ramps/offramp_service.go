package ramps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/support/queuejob"
)

// limitTokenScale is display precision, not the token's. "3.66" is a number a
// user reads once and types back; "3.652174" is one they check digit by digit.
// Two places are safe at either bound because SourceForMinimum rounds up and
// SourceForMaximum rounds down, each away from the bound it came from.
const limitTokenScale = 2

// receiveFiatScale is the scale every adapter rounds the BRL leg of a quote
// at. The conversion needs it to tell the quote apart from its own rounding.
const receiveFiatScale = 2

// withTokenLimit fills a limit refusal's token equivalent, so the refusal can
// say what to type instead of only what the bound is in BRL.
//
// The rail's bound is BRL because the PIX leg settles in BRL, but the off-ramp
// form is denominated in a token. Without this the user is handed a target in
// a unit they are not typing in, and converting it themselves needs a rate
// that is not on the screen.
//
// Any failure leaves err untouched. A refusal missing its conversion is a
// poorer message; a refusal that turned into an internal error is a broken
// screen, and this is the error path already.
func withTokenLimit(err error, sourceAmount, receiveAmount string) error {
	var violation *ramp.LimitViolation
	if !errors.As(err, &violation) {
		return err
	}

	// The two bounds are opposite questions: the floor asks for the smallest
	// amount that clears it, the cap for the largest that fits under it.
	convert := ramp.SourceForMinimum
	if errors.Is(violation.Err, ramp.ErrAmountAboveMaximum) {
		convert = ramp.SourceForMaximum
	}

	quoted := ramp.QuotedAmounts{
		Source:       sourceAmount,
		Receive:      receiveAmount,
		ReceiveScale: receiveFiatScale,
	}
	converted, convErr := convert(quoted, violation.LimitBRL, limitTokenScale)
	if convErr != nil {
		facades.Log().Warningf("ramps: convert limit %s BRL to token: %v", violation.LimitBRL, convErr)
		return err
	}

	violation.LimitToken = converted
	return err
}

// OfframpService turns crypto into BRL on a PIX key.
type OfframpService struct {
	engine
	destinations repositories.OfframpDestinationRepository
	mfaStepUp    MfaStepUpValidator
	appEnv       string
	settleJob    queuejob.Deferred
	// accounts is the bank rails' registered-account source (Deps.BeneficiaryAccounts).
	accounts BankAccountEnsurer
	// beneficiaries is the bank beneficiary book a bank create reads the
	// current revision from (Deps.Beneficiaries).
	beneficiaries BankBeneficiaryReader
	// pixAccounts is the Pix destination's registrar (Deps.PixAccounts).
	pixAccounts PixAccountEnsurer
}

// NewOfframpService wires the off-ramp service. The extra collaborators are the
// ones only a payout needs: the step-up, the destination registry it caches
// against, and the environment that decides whether a deposit can be simulated.
func NewOfframpService(
	d Deps,
	destinations repositories.OfframpDestinationRepository,
	mfaStepUp MfaStepUpValidator,
	appEnv string,
	settleJob queuejob.Deferred,
) *OfframpService {
	return &OfframpService{
		engine:       newEngine(d),
		destinations: destinations, mfaStepUp: mfaStepUp, appEnv: appEnv,
		settleJob: settleJob,
		accounts:  d.BeneficiaryAccounts, beneficiaries: d.Beneficiaries, pixAccounts: d.PixAccounts,
	}
}

// Estimate quotes the customer's payout, asked for by user, and persists the
// quote. A bank rail is quoted by estimateBank; an empty rail is Pix.
func (s *OfframpService) Estimate(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.EstimateOfframpDTO,
) (*EstimateView, error) {
	if ramp.Rail(in.Rail).IsBank() {
		return s.estimateBank(ctx, user, customer, in)
	}
	// Normalized before anything reads it, so the snapshot this quote stores and
	// the create that has to match it are already speaking the same string.
	in.RecipientName = normalizeRecipientName(in.RecipientName)
	if err := requireRail(ctx, s.features, ramp.RailPixPayout, user.ID); err != nil {
		return nil, err
	}
	setting, err := railPolicy(s.config, ramp.RailPixPayout)
	if err != nil {
		return nil, err
	}

	// Per attempt, reset by the callback: a failover from a provider that
	// prepares funding to one that does not must not carry its account along.
	var (
		prepared  bool
		pixBankID string
		pixCustID string
		remaining string
		capped    bool
	)
	estimate, provider, err := s.quoteWith(ctx, customer.ID,
		ramp.Capability{
			Rail: ramp.RailPixPayout, Token: in.Token, Chain: in.Chain,
			PreparedFunding: slices.Contains(in.FundingFormats, FundingFormatPrepared),
		}, failoverUnlessPixAccountRefused,
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) (*ramp.Estimate, error) {
			prepared, pixBankID, pixCustID, remaining, capped = false, "", "", "", false
			// A business's money rules, after the router's gates and before
			// the provider is asked (API contract §10).
			if err := s.rules.offramp(customer, in.Chain, in.FromAddress, in.PixKeyType, in.PixKey); err != nil {
				return nil, err
			}
			// The holder the quote — and the order bound to it — names.
			holder, err := s.rules.holder(customer, in.RecipientName)
			if err != nil {
				return nil, err
			}
			in.RecipientName = holder
			// Resolved per attempt: a failover prices with the fee of the
			// provider it fails over to.
			fee, err := setting.FeeFor(adapter.Code())
			if err != nil {
				return nil, err
			}
			request := offrampQuoteRequest(in, fee)
			// A provider that prepares funding is registered a destination
			// first (D4, D5), then quotes it.
			if _, prepares := adapter.(ramp.OfframpPrepareAdapter); !prepares {
				return adapter.EstimateOfframp(ctx, ref, request)
			}
			registrar, ok := adapter.(ramp.PixAccountRegistrar)
			if !ok || s.pixAccounts == nil {
				return nil, fmt.Errorf("ramps: %s registers no Pix account: %w", adapter.Code(), ramp.ErrOfframpCreateUnavailable)
			}
			baID, err := s.pixAccounts.Ensure(ctx, registrar, ref, customer, pixAccountInput(adapter.Code(), in))
			if err != nil {
				return nil, err
			}
			request.Destination.ExternalAccountID = baID
			estimate, err := adapter.EstimateOfframp(ctx, ref, request)
			if err != nil {
				return nil, err
			}
			if reader, ok := adapter.(ramp.PayoutLimitReader); ok {
				left, err := reader.RemainingPayoutUSD(ctx, ref)
				if err != nil {
					return nil, err
				}
				remaining, capped = left, true
			}
			prepared, pixBankID, pixCustID = true, baID, ref.ExternalID
			return estimate, nil
		})
	if err != nil {
		return nil, err
	}
	fee, err := setting.FeeFor(provider)
	if err != nil {
		return nil, err
	}

	// What the quote priced, which is not always what was typed: Bridge moves
	// two places of a six-place token. The response shows it and the create is
	// bound to it, so the user confirms the amount that actually moves.
	sourceAmount := cmp.Or(estimate.SourceAmount, in.AmountToken)

	// Off-ramp the user sends crypto, so the BRL the rail limits are expressed
	// in is only knowable once the quote exists. The check therefore happens
	// here rather than on the request — which is also what puts the rate in
	// scope to express the bound back in the token the user typed.
	if err := checkLimits(setting, estimate.EstimatedReceive, in.Token); err != nil {
		return nil, withTokenLimit(err, sourceAmount, estimate.EstimatedReceive)
	}
	// The provider's own allowance is in dollars of what leaves the wallet,
	// whatever the rail's limits are counted in (D9).
	if capped {
		if err := checkRemainingUSD(remaining, sourceAmount); err != nil {
			return nil, err
		}
	}

	// Read before the quote is stored, from the same source Create reads, so
	// the quote never names a mode its order would not have. A prepared order
	// has no deposit mode to read: it is funded by authorization.
	mode := DepositModePrepared
	if !prepared {
		if mode, err = DepositModeForProvider(s.config, provider); err != nil {
			return nil, err
		}
	}
	fundingMode := ""
	if prepared {
		fundingMode = models.FundingModePrepared
	}
	view, err := s.persistEstimate(customer.ID, user.ID, provider, ramp.RailPixPayout, estimate, estimateSnapshot{
		Rail:                     ramp.RailPixPayout.String(),
		SourceAmount:             sourceAmount,
		SourceCurrency:           in.Token,
		DestCurrency:             brl,
		DestPaymentRail:          pixRail,
		PixKey:                   in.PixKey,
		PixKeyType:               in.PixKeyType,
		RecipientName:            in.RecipientName,
		RecipientDocument:        in.RecipientDocument,
		Chain:                    in.Chain,
		FromAddress:              in.FromAddress,
		Rate:                     estimate.Rate,
		FeePercent:               fee,
		FeeAmount:                estimate.FeeAmount,
		FeeCurrency:              estimate.FeeCurrency,
		EstimatedReceive:         estimate.EstimatedReceive,
		ProviderDeductedAmount:   estimate.ProviderDeductedAmount,
		ProviderDeductedCurrency: estimate.ProviderDeductedCurrency,
		ProviderFeePercent:       estimate.ProviderFeePercent,
		ProviderFeeAmount:        estimate.ProviderFeeAmount,
		BankAccountID:            pixBankID,
		CustomerExternalID:       pixCustID,
		FundingMode:              fundingMode,
	})
	if err != nil {
		return nil, err
	}
	view.DepositMode = mode
	view.DestinationCurrency = brl
	if mode == DepositModeOnChain {
		// The order's own address arrives only with the order, and only then
		// could the app tell whether the deposit also creates the address's
		// token account (the sender pays its rent). Bridge gives a customer
		// one address — the customer's, not its member's — so the last one is
		// where this deposit goes; with none yet, the account does not exist
		// either.
		view.DepositToAddress, err = s.txRepo.FindLatestOfframpDepositAddress(customer.ID, provider, in.Token)
		if err != nil {
			return nil, fmt.Errorf("ramps: find offramp deposit address: %w", err)
		}
	}
	return view, nil
}

// FundingFormatPrepared is the funding format an app declares when it can sign
// the transaction a provider prepared; "deposit" is the default and needs no
// declaration.
const FundingFormatPrepared = "prepared"

// pixAccountInput is the destination the quote registers at the provider.
func pixAccountInput(provider ramp.ProviderCode, in dto.EstimateOfframpDTO) dto.RegisterPixAccountDTO {
	return dto.RegisterPixAccountDTO{
		Provider: string(provider), PixKey: in.PixKey, PixKeyType: in.PixKeyType,
		RecipientName: in.RecipientName, RecipientDocument: in.RecipientDocument,
	}
}

// offrampQuoteRequest is what the off-ramp is priced with. Estimate and Create both
// build it here, so the payout is placed for exactly what was quoted.
func offrampQuoteRequest(in dto.EstimateOfframpDTO, feePercent string) ramp.OfframpQuoteRequest {
	return ramp.OfframpQuoteRequest{
		Source: ramp.CryptoLeg{
			Currency: in.Token, PaymentRail: in.Chain,
			Amount: in.AmountToken, Address: in.FromAddress,
		},
		Destination: ramp.PixDestination{
			Rail: pixRail, Currency: brl,
			PixKey: in.PixKey, PixKeyType: in.PixKeyType,
			AccountOwnerName:  in.RecipientName,
			RecipientDocument: in.RecipientDocument,
		},
		FeePercent: feePercent,
	}
}

// Create places the customer's off-ramp order for user.
//
// An estimate is required. The rail's limits are expressed in BRL and the BRL
// leg of an off-ramp is only knowable through a quote — without one the
// configured minimum and maximum could not be enforced at all, and shipping a
// money-out path that silently skips its own limits is not a trade worth
// making. A bank rail is placed by createBank; an empty rail is Pix.
func (s *OfframpService) Create(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
) (*OrderView, bool, error) {
	if ramp.Rail(in.Rail).IsBank() {
		return s.createBank(ctx, user, customer, in)
	}
	// Same normalization the estimate applied, and for the same reason: the two
	// are compared byte for byte, and a trailing space is not another person.
	in.RecipientName = normalizeRecipientName(in.RecipientName)
	// A person's step-up comes first: an off-ramp sends money to an arbitrary
	// PIX key, so nothing else should happen until the user has proven they
	// are present. A business's comes after its gates (businessGates).
	if stepUpFirst(customer) {
		if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
			return nil, false, err
		}
	}
	if err := requireRail(ctx, s.features, ramp.RailPixPayout, user.ID); err != nil {
		return nil, false, err
	}
	setting, err := railPolicy(s.config, ramp.RailPixPayout)
	if err != nil {
		return nil, false, err
	}
	if in.EstimateID == "" {
		return nil, false, ramp.ErrEstimateRequired
	}

	estimateRow, snapshot, err := s.loadEstimateRow(customer.ID, in.EstimateID, ramp.RailPixPayout.String())
	if err != nil {
		return nil, false, err
	}
	if estimateRow.IsConsumed() {
		// A double tap on a prepared quote lands on its order and action;
		// any other consumed quote is refused as it always was.
		view, err := s.replayConsumedPix(customer, in, estimateRow, snapshot)
		return view, false, err
	}
	if estimateRow.IsExpired() {
		return nil, false, ramp.ErrEstimateExpired
	}
	if err := checkLimits(setting, snapshot.EstimatedReceive, snapshot.SourceCurrency); err != nil {
		return nil, false, withTokenLimit(err, snapshot.SourceAmount, snapshot.EstimatedReceive)
	}
	// A company's quote named the company as the holder; the order is bound
	// to the same name, whatever the request typed.
	if in.RecipientName, err = s.rules.holder(customer, in.RecipientName); err != nil {
		return nil, false, err
	}
	if err := bindOfframpCreate(in, snapshot); err != nil {
		return nil, false, err
	}
	fee, err := setting.FeeFor(estimateRow.Provider)
	if err != nil {
		return nil, false, err
	}
	// The bind compares the amounts as numbers, and a provider may count the
	// places of the string ("3.7500" is refused by Bridge where "3.75" is not):
	// the order carries the quote's own spelling of the amount.
	in.AmountToken = snapshot.SourceAmount

	// A quote a provider prepared the funding of is placed by authorization,
	// not by a deposit to an address.
	if snapshot.FundingMode == models.FundingModePrepared {
		return s.createPreparedPix(ctx, user, customer, in, estimateRow, snapshot, fee)
	}

	if early, err := s.dedupByEstimate(customer.ID, estimateRow, models.TransactionTypeOfframp); err != nil {
		return nil, false, err
	} else if early != nil && !stale(early) {
		return s.toOrderView(early), false, nil
	}

	// Identified by what the key normalizes to, so an earlier order to it typed
	// another way — with or without +55, punctuated or not — still counts. It
	// is the customer's: the provider registered it on the customer's own.
	destination := models.OfframpDestination{
		CustomerID:   customer.ID,
		PixKeyHash:   pixkeys.HashKey(in.PixKeyType, in.PixKey),
		DocumentHash: pixkeys.DestinationDocumentHash(in.PixKeyType, in.RecipientDocument),
	}

	// Same shape as the on-ramp: the lock covers "check dedup, reserve the
	// row", and the provider call runs after the transaction has committed.
	var (
		existing       *models.Transaction
		tx             *models.Transaction
		provider       ramp.ProviderAdapter
		ref            ramp.CustomerRef
		destinationRef string
		resumed        bool
	)
	err = s.locker.WithLock(ctx, createLockKey(models.TransactionTypeOfframp, customer.ID, in.EstimateID),
		func(ctx context.Context) error {
			found, err := s.dedupByEstimate(customer.ID, estimateRow, models.TransactionTypeOfframp)
			if err != nil {
				return err
			}
			if found != nil && !stale(found) {
				existing = found
				return nil
			}

			// Sticky to the quote's provider, and no failover on the call
			// that follows: see the same step in the on-ramp's Create.
			provider, ref, err = s.router.ResolveRedemption(ctx, customer.ID, ramp.RailPixPayout, estimateRow.Provider)
			if err != nil {
				return err
			}
			if err := s.businessGates(ctx, user, customer, in); err != nil {
				return err
			}

			// A key an earlier order already registered with this provider is
			// named, not registered again. Bridge answers a second registration
			// with a 422 once the first call's idempotency key has expired, so
			// without this every withdrawal after the first day to the same key
			// failed. Read before the reservation, so a failed read leaves
			// nothing behind.
			destination.Provider = provider.Code()
			destinationRef, err = s.destinations.FindRef(destination)
			if err != nil {
				return fmt.Errorf("ramps: find offramp destination: %w", err)
			}
			if found != nil {
				tx, resumed = found, true
				return s.resume(tx)
			}

			tx = newTransaction(customer.ID, user.ID, provider.Code(), models.TransactionTypeOfframp, ramp.RailPixPayout)
			tx.AmountFiat = snapshot.EstimatedReceive
			tx.AmountToken = in.AmountToken
			tx.TargetToken = in.Token
			// The crypto leaves the user's own wallet and lands at the provider's
			// deposit address, which arrives with the created order.
			tx.FromAddress = &in.FromAddress
			tx.Chain = &in.Chain
			tx.PixKey = &in.PixKey
			tx.PixKeyType = &in.PixKeyType
			if in.RecipientName != "" {
				tx.RecipientName = &in.RecipientName
			}
			if in.RecipientDocument != "" {
				tx.RecipientDocument = &in.RecipientDocument
			}
			tx.FeePercent = &fee
			tx.EstimateID = &estimateRow.ID
			mode, err := DepositModeForProvider(s.config, provider.Code())
			if err != nil {
				return err
			}
			tx.DepositMode = &mode
			if err := s.reserve(tx); err != nil {
				return fmt.Errorf("ramps: persist offramp: %w", err)
			}
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return s.toOrderView(existing), false, nil
	}

	order, err := provider.CreateOfframp(ctx, ref, ramp.OfframpCreateRequest{
		OfframpQuoteRequest:         offrampQuoteRequest(in.EstimateOfframpDTO, fee),
		ProviderQuoteID:             deref(estimateRow.ProviderQuoteID),
		ExternalAccountID:           destinationRef,
		IdempotencyKey:              *tx.IdempotencyKey,
		QuoteExpiresAt:              estimateRow.ExpiresAt,
		OfframpFundingWindowMinutes: setting.OzavOfframpFundingWindowMinutes,
	})
	if err != nil {
		// A resumed row's first attempt is unknown state, whatever this one says.
		if !resumed {
			s.releaseReservation(tx, err)
		}
		return nil, false, fmt.Errorf("ramps: create offramp: %w", err)
	}

	// The provider registered the key with this order, and recording it is
	// what lets the next order name it. The transfer already exists upstream,
	// so a failure is logged rather than answered with an error the client
	// would retry.
	if destinationRef == "" && order.ProviderDestinationRef != "" {
		destination.DestinationRef = order.ProviderDestinationRef
		if err := s.destinations.Upsert(destination); err != nil {
			facades.Log().WithContext(ctx).With(map[string]any{"transaction": tx.ID}).
				Errorf("ramps: record offramp destination: %v", err)
		}
	}

	applyCreatedOrder(tx, order)
	if address, ok := order.DepositInstructions["to_address"].(string); ok && address != "" {
		tx.DestinationWallet = address
	}
	if order.ProviderDestinationRef != "" {
		tx.ProviderDestinationRef = &order.ProviderDestinationRef
	}
	if err := s.txRepo.Update(tx); err != nil {
		return nil, false, fmt.Errorf("ramps: persist offramp order: %w", err)
	}

	// Logged rather than returned: the order exists upstream, and refusing to
	// return it because the bookkeeping lagged would be the worse outcome.
	if err := s.estimateRepo.MarkConsumed(estimateRow); err != nil {
		facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID, "estimate": estimateRow.ID}).
			Errorf("ramps: mark estimate consumed: %v", err)
	}
	s.publish(tx)
	return s.toOrderView(tx), true, nil
}

// businessGates is what a business's payout owes once its local gates —
// rail, estimate, limits, the redemption's router — have passed, and before
// anything is reserved (API contract §10): its money rules, then the step-up,
// so a wrong key never spends an MFA attempt. A person owes neither here: their
// step-up came first, and none of these rules is theirs.
func (s *OfframpService) businessGates(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
) error {
	if stepUpFirst(customer) {
		return nil
	}
	if err := s.rules.offramp(customer, in.Chain, in.FromAddress, in.PixKeyType, in.PixKey); err != nil {
		return err
	}
	return enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType)
}
