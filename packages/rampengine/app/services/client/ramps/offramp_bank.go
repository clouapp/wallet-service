package ramps

import (
	"cmp"
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/bank"
)

// ErrBankBeneficiaryNotFound is a bank payout naming a beneficiary that is not
// the customer's or does not exist. It aliases the bank service's value, so
// errors.Is holds through the wrapping and the controller never imports that
// package.
var ErrBankBeneficiaryNotFound = bank.ErrBankBeneficiaryNotFound

// usd is the fiat a bank rail pays out in.
const usd = "usd"

// BankAccountEnsurer answers the approved provider bank account of a
// beneficiary on a rail, registering it first if it has to. Production adapts
// bank.ProviderAccountService to it (app/providers), whose errors pass through
// unchanged.
//
// It answers ramp.BankPayoutAccount (bank.ProviderAccount is an alias of it)
// so its mock never imports the bank service, whose own tests import
// tests/mocks.
type BankAccountEnsurer interface {
	Ensure(ctx context.Context, customerID uint, beneficiaryExternalID string, provider ramp.ProviderCode, rail ramp.Rail) (*ramp.BankPayoutAccount, error)
}

// noFailover is the bank payout's failover policy: none. The account the
// quote pays out to is registered with the provider that answered, so a
// provider that failed is not one the next can stand in for (spec §5.6).
func noFailover(error) bool { return false }

// bankPaymentRail is the destination payment rail a bank rail's snapshot
// names: the provider's own word for it.
func bankPaymentRail(rail ramp.Rail) string {
	if rail == ramp.RailWirePayout {
		return "wire"
	}
	return "ach"
}

// estimateBank quotes the customer's payout to a registered US bank account,
// asked for by user, and persists the quote (spec §5.6 item 3).
//
// The account is ensured inside the resolve, with the provider the router
// picked: the beneficiary is registered as that provider's customer's, and
// only the callback knows the ref. Its errors leave unchanged — the envelope
// already names them (bank_account_verifying, bank_account_rejected).
func (s *OfframpService) estimateBank(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.EstimateOfframpDTO,
) (*EstimateView, error) {
	rail := ramp.Rail(in.Rail)
	// A service built without the port cannot reach a bank account; saying
	// so beats a nil dereference inside the resolve.
	if s.accounts == nil {
		return nil, fmt.Errorf("ramps: bank accounts not wired: %w", ramp.ErrNoProviderAvailable)
	}
	if err := requireRail(ctx, s.features, rail, user.ID); err != nil {
		return nil, err
	}
	policy, err := railPolicy(s.config, rail)
	if err != nil {
		return nil, err
	}

	var (
		account   *ramp.BankPayoutAccount
		remaining string
		capped    bool
	)
	estimate, provider, err := s.quoteWith(ctx, customer.ID,
		ramp.Capability{Rail: rail, Token: in.Token, Chain: in.Chain}, noFailover,
		func(ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef) (*ramp.Estimate, error) {
			// A business's money rules, after the router's gates and before
			// any account is registered or the provider is asked (API
			// contract §10), as on Pix.
			if err := s.rules.bankOfframp(customer, in.Chain, in.FromAddress); err != nil {
				return nil, err
			}
			ensured, err := s.accounts.Ensure(ctx, customer.ID, in.BankBeneficiaryID, adapter.Code(), rail)
			if err != nil {
				return nil, err
			}
			fee, err := policy.FeeFor(adapter.Code())
			if err != nil {
				return nil, err
			}
			estimate, err := adapter.EstimateOfframp(ctx, ref, ramp.OfframpQuoteRequest{
				Source: ramp.CryptoLeg{
					Currency: in.Token, PaymentRail: in.Chain,
					Amount: in.AmountToken, Address: in.FromAddress,
				},
				Bank:       &ramp.BankDestination{Rail: rail, Currency: usd, ExternalAccountID: ensured.ExternalAccountID},
				FeePercent: fee,
			})
			if err != nil {
				return nil, err
			}
			// A provider that knows the customer's remaining allowance caps
			// the quote with it; one that does not caps nothing.
			if reader, ok := adapter.(ramp.PayoutLimitReader); ok {
				left, err := reader.RemainingPayoutUSD(ctx, ref)
				if err != nil {
					return nil, err
				}
				remaining, capped = left, true
			}
			account = ensured
			return estimate, nil
		})
	if err != nil {
		return nil, err
	}
	fee, err := policy.FeeFor(provider)
	if err != nil {
		return nil, err
	}

	// What the quote priced, falling back to what was typed. The bank rails
	// are bounded in USD of the source stablecoin (spec E0 item 5), and so is
	// the provider's allowance: both are checked against what leaves the
	// wallet, not what lands.
	sourceAmount := cmp.Or(estimate.SourceAmount, in.AmountToken)
	if err := checkLimitsUSD(policy, sourceAmount); err != nil {
		return nil, err
	}
	if capped {
		if err := checkRemainingUSD(remaining, sourceAmount); err != nil {
			return nil, err
		}
	}

	view, err := s.persistEstimate(customer.ID, user.ID, provider, rail, estimate, estimateSnapshot{
		Rail:                rail.String(),
		SourceAmount:        sourceAmount,
		SourceCurrency:      in.Token,
		DestCurrency:        usd,
		DestPaymentRail:     bankPaymentRail(rail),
		Chain:               in.Chain,
		FromAddress:         in.FromAddress,
		Rate:                estimate.Rate,
		FeePercent:          fee,
		FeeAmount:           estimate.FeeAmount,
		FeeCurrency:         estimate.FeeCurrency,
		EstimatedReceive:    estimate.EstimatedReceive,
		ProviderFeePercent:  estimate.ProviderFeePercent,
		ProviderFeeAmount:   estimate.ProviderFeeAmount,
		BankBeneficiaryID:   in.BankBeneficiaryID,
		BeneficiaryRevision: account.BeneficiaryRevision,
		BankAccountID:       account.ExternalAccountID,
		CustomerExternalID:  account.CustomerExternalID,
	})
	if err != nil {
		return nil, err
	}
	view.DestinationCurrency = usd
	return view, nil
}

// checkRemainingUSD refuses a payout above what the provider still lets the
// customer pay out, naming that allowance as the bound.
func checkRemainingUSD(remaining, amountUSD string) error {
	amount, ok := new(big.Rat).SetString(amountUSD)
	if !ok {
		return fmt.Errorf("ramps: amount %q is not a number", amountUSD)
	}
	ceiling, ok := new(big.Rat).SetString(remaining)
	if !ok {
		return fmt.Errorf("ramps: remaining payout %q is not a number", remaining)
	}
	if amount.Cmp(ceiling) > 0 {
		return &ramp.LimitViolation{LimitUSD: remaining, Err: ramp.ErrAmountAboveMaximum}
	}
	return nil
}
