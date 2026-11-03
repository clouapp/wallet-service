package ramps

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// OnrampService turns BRL into crypto in the user's own wallet.
//
// The rail has two shapes, and which one a provider serves is the provider's
// property rather than the user's choice:
//
//   - Deposit-first (Bridge): BRL is collected through a standing account
//     whose code is permanent and carries no amount, so there is no order to
//     place — the deposit webhook creates the transaction. That path is
//     VirtualAccountService's, and Create refuses there because the adapter
//     does.
//   - Order-first (Ozav): each pay-in is its own transfer, answered with a Pix
//     BR Code that carries this amount and expires. That is what Create places.
//
// Both live behind the same rail because the router picks the provider; a
// client that has an estimate calls Create, and one whose provider collects
// through an account gets told so by the error rather than by a second
// endpoint it had to know about in advance.
type OnrampService struct {
	engine
}

// NewOnrampService wires the on-ramp service.
func NewOnrampService(d Deps) *OnrampService {
	return &OnrampService{engine: newEngine(d)}
}

// Estimate quotes the customer's order, asked for by user, and persists the
// quote.
//
// The quote is persisted even when the provider's own estimate is indicative,
// because the row is what makes the provider sticky between here and the
// create, what snapshots the fee the user was shown, and what the create
// deduplicates on.
func (s *OnrampService) Estimate(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.EstimateOnrampDTO,
) (*EstimateView, error) {
	if err := requireRail(ctx, s.features, ramp.RailPixPayin, user.ID); err != nil {
		return nil, err
	}
	setting, err := railPolicy(s.config, ramp.RailPixPayin)
	if err != nil {
		return nil, err
	}
	// On-ramp the user sends BRL, so the rail's limits apply to the request
	// itself and are checked before any provider is called.
	if err := checkLimits(setting, in.AmountBRL, in.Token); err != nil {
		return nil, err
	}

	estimate, provider, err := s.quote(ctx, customer.ID,
		ramp.Capability{Rail: ramp.RailPixPayin, Token: in.Token, Chain: in.Chain},
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) (*ramp.Estimate, error) {
			// A business's money rules, after the router's gates and before
			// the provider is asked (API contract §10).
			if err := s.rules.onramp(customer, in.Chain, in.Wallet); err != nil {
				return nil, err
			}
			// Resolved per attempt: a failover prices with the fee of the
			// provider it fails over to.
			fee, err := setting.FeeFor(adapter.Code())
			if err != nil {
				return nil, err
			}
			return adapter.EstimateOnramp(ctx, ref, onrampQuoteRequest(in, fee))
		})
	if err != nil {
		return nil, err
	}
	// The fee the user is shown is the rail's for the provider that answered,
	// never whatever fee the adapter echoed.
	fee, err := setting.FeeFor(provider)
	if err != nil {
		return nil, err
	}

	return s.persistEstimate(customer.ID, user.ID, provider, ramp.RailPixPayin, estimate, estimateSnapshot{
		Rail:                     ramp.RailPixPayin.String(),
		SourceAmount:             in.AmountBRL,
		SourceCurrency:           brl,
		DestCurrency:             in.Token,
		DestPaymentRail:          in.Chain,
		DestAddress:              in.Wallet,
		Rate:                     estimate.Rate,
		FeePercent:               fee,
		FeeAmount:                estimate.FeeAmount,
		FeeCurrency:              estimate.FeeCurrency,
		EstimatedReceive:         estimate.EstimatedReceive,
		ProviderDeductedAmount:   estimate.ProviderDeductedAmount,
		ProviderDeductedCurrency: estimate.ProviderDeductedCurrency,
		ProviderFeePercent:       estimate.ProviderFeePercent,
		ProviderFeeAmount:        estimate.ProviderFeeAmount,
	})
}

// onrampQuoteRequest is what the on-ramp is priced with. Estimate and Create both
// build it here, so the order is placed for exactly what was quoted.
func onrampQuoteRequest(in dto.EstimateOnrampDTO, feePercent string) ramp.OnrampQuoteRequest {
	return ramp.OnrampQuoteRequest{
		Source:      ramp.FiatLeg{Rail: pixRail, Currency: brl, Amount: in.AmountBRL},
		Destination: ramp.CryptoLeg{Currency: in.Token, PaymentRail: in.Chain, Address: in.Wallet},
		FeePercent:  feePercent,
	}
}

// Create places the customer's on-ramp order for user and returns the
// instructions that fund it.
//
// An estimate is required, which the deposit-first shape of this rail never
// needed. The reason is the provider's quote: it is binding and it is what
// fixes the rate the user was shown, so a create without one would hand the
// user a BR Code priced at whatever the rate is when it lands.
func (s *OnrampService) Create(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOnrampDTO,
) (*OrderView, bool, error) {
	if err := requireRail(ctx, s.features, ramp.RailPixPayin, user.ID); err != nil {
		return nil, false, err
	}
	setting, err := railPolicy(s.config, ramp.RailPixPayin)
	if err != nil {
		return nil, false, err
	}
	// On-ramp the user sends BRL, so the rail's limits are answerable from the
	// request itself and are checked before anything is loaded or called. The
	// off-ramp has to wait for a quote for the same check.
	if err := checkLimits(setting, in.AmountBRL, in.Token); err != nil {
		return nil, false, err
	}
	if in.EstimateID == "" {
		return nil, false, ramp.ErrEstimateRequired
	}

	estimateRow, snapshot, err := s.loadEstimate(customer.ID, in.EstimateID, ramp.RailPixPayin.String())
	if err != nil {
		return nil, false, err
	}
	if err := bindOnrampCreate(in, snapshot); err != nil {
		return nil, false, err
	}
	// The current fee of the provider the quote came from (spec §2): the order
	// is placed with that provider, so it is charged that provider's fee.
	fee, err := setting.FeeFor(estimateRow.Provider)
	if err != nil {
		return nil, false, err
	}

	if early, err := s.dedupByEstimate(customer.ID, estimateRow, models.TransactionTypeOnramp); err != nil {
		return nil, false, err
	} else if early != nil && !stale(early) {
		return s.toOrderView(early), false, nil
	}

	// Same shape as the off-ramp: the lock covers "check dedup, reserve the
	// row", and the provider call runs after the transaction has committed.
	var (
		existing *models.Transaction
		tx       *models.Transaction
		provider ramp.ProviderAdapter
		ref      ramp.CustomerRef
		resumed  bool
	)
	err = s.locker.WithLock(ctx, createLockKey(models.TransactionTypeOnramp, customer.ID, in.EstimateID),
		func(ctx context.Context) error {
			found, err := s.dedupByEstimate(customer.ID, estimateRow, models.TransactionTypeOnramp)
			if err != nil {
				return err
			}
			if found != nil && !stale(found) {
				existing = found
				return nil
			}

			// The quote the user was shown came from one provider, so the
			// create is placed with that one; redeeming it anywhere else would
			// price the order differently from what they agreed to. There is
			// no failover on the call that follows either: a create moves
			// money, a timeout is unknown state, and trying a second provider
			// is how one payment becomes two.
			provider, ref, err = s.router.ResolveRedemption(ctx, customer.ID, ramp.RailPixPayin, estimateRow.Provider)
			if err != nil {
				return err
			}
			if err := s.rules.onramp(customer, in.Chain, in.Wallet); err != nil {
				return err
			}
			if found != nil {
				tx, resumed = found, true
				return s.resume(tx)
			}

			tx = newTransaction(customer.ID, user.ID, provider.Code(), models.TransactionTypeOnramp, ramp.RailPixPayin)
			tx.AmountFiat = in.AmountBRL
			tx.AmountToken = cmp.Or(snapshot.EstimatedReceive, "0")
			// Uppercase is the token vocabulary the client reads, the same
			// normalization the deposit-first path applies to the account's
			// lowercase echo.
			tx.TargetToken = strings.ToUpper(in.Token)
			// The crypto is delivered to the user's own wallet: XIP never takes
			// custody of it.
			tx.DestinationWallet = in.Wallet
			// Persisted because the delivery has to name the network it sends
			// on, and it would otherwise live only in the estimate's snapshot.
			tx.Chain = &in.Chain
			tx.FeePercent = &fee
			tx.EstimateID = &estimateRow.ID
			mode, err := DepositModeForProvider(s.config, provider.Code())
			if err != nil {
				return err
			}
			tx.DepositMode = &mode
			if err := s.reserve(tx); err != nil {
				return fmt.Errorf("ramps: persist onramp: %w", err)
			}
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return s.toOrderView(existing), false, nil
	}

	order, err := provider.CreateOnramp(ctx, ref, ramp.OnrampCreateRequest{
		OnrampQuoteRequest: onrampQuoteRequest(in.EstimateOnrampDTO, fee),
		ProviderQuoteID:    deref(estimateRow.ProviderQuoteID),
		IdempotencyKey:     *tx.IdempotencyKey,
	})
	if err != nil {
		// A resumed row's first attempt is unknown state, whatever this one says.
		if !resumed {
			s.releaseReservation(tx, err)
		}
		return nil, false, fmt.Errorf("ramps: create onramp: %w", err)
	}

	applyCreatedOrder(tx, order)
	if err := s.txRepo.Update(tx); err != nil {
		return nil, false, fmt.Errorf("ramps: persist onramp order: %w", err)
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
