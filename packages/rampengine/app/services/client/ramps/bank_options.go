package ramps

import (
	"context"
	"errors"
	"math/big"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Why a bank rail is not offered, as the app words it.
const (
	reasonFlagClosed          = "flag_closed"
	reasonTosRequired         = "tos_required"
	reasonKycRequired         = "kyc_required"
	reasonProviderUnavailable = "provider_unavailable"
)

const (
	// minBankPayoutUSD is the smallest bank payout the app offers, whatever
	// the rail's own minimum says: below it the provider's fee eats the payout.
	minBankPayoutUSD = "5.00"
	// bankOptionsChain is the chain a bank payout is offered on.
	bankOptionsChain = models.ChainSolana
)

// BankOptionsView is what the bank payout screen shows before a quote.
type BankOptionsView struct {
	Rails []BankRailOption
}

// BankRailOption is one bank rail as the customer meets it. Reason is empty
// when the rail is available. MinUSD and MaxUSD are empty on a rail with no
// such bound.
type BankRailOption struct {
	Rail      string
	Available bool
	Reason    string
	Tokens    []string
	Chain     string
	MinUSD    string
	MaxUSD    string
	// RequiresSolForFee is true when the customer's own wallet pays the
	// network fee, so it must hold SOL: the delegation transaction names the
	// owner as fee payer. False on a rail that is not offered.
	RequiresSolForFee bool
}

// BankOptions answers, for each bank rail, whether the customer can quote it
// now and between which amounts.
//
// Every refusal here is an answer, not an error: the screen has to render
// whatever the state, so only a failure of ours — the settings, the router's
// own reads — leaves as one. The provider's allowance is the exception that
// proves it: a provider that cannot be read makes the rail unavailable
// instead of taking the screen down.
func (s *OfframpService) BankOptions(
	ctx context.Context, user *models.User, customer *models.Customer,
) (*BankOptionsView, error) {
	rails := ramp.BankRails()
	view := &BankOptionsView{Rails: make([]BankRailOption, 0, len(rails))}
	for _, rail := range rails {
		option, err := s.bankRailOption(ctx, user, customer, rail)
		if err != nil {
			return nil, err
		}
		view.Rails = append(view.Rails, *option)
	}
	return view, nil
}

func (s *OfframpService) bankRailOption(
	ctx context.Context, user *models.User, customer *models.Customer, rail ramp.Rail,
) (*BankRailOption, error) {
	closed := func(reason string) *BankRailOption {
		return &BankRailOption{Rail: rail.String(), Reason: reason}
	}
	if err := requireRail(ctx, s.features, rail, user.ID); err != nil {
		if errors.Is(err, ramp.ErrRailDisabled) {
			return closed(reasonFlagClosed), nil
		}
		return nil, err
	}

	// No token: the rail is open when any token is quotable on it, and the
	// loop below lists which.
	adapter, ref, err := s.router.Resolve(ctx, customer.ID,
		ramp.Capability{Rail: rail, Chain: bankOptionsChain})
	if err != nil {
		if reason, ok := bankRefusal(err); ok {
			return closed(reason), nil
		}
		return nil, err
	}

	policy, err := railPolicy(s.config, rail)
	if err != nil {
		if errors.Is(err, ramp.ErrNoProviderAvailable) {
			return closed(reasonProviderUnavailable), nil
		}
		return nil, err
	}

	option := &BankRailOption{Rail: rail.String(), Available: true, Chain: bankOptionsChain,
		RequiresSolForFee: true}
	for _, token := range models.RampTokens {
		if adapter.Supports(ramp.Capability{Rail: rail, Token: token, Chain: bankOptionsChain}) {
			option.Tokens = append(option.Tokens, token)
		}
	}
	option.MinUSD = largerUSD(minBankPayoutUSD, policy.MinUSD)

	ceiling := policy.MaxUSD
	if reader, ok := adapter.(ramp.PayoutLimitReader); ok {
		remaining, err := reader.RemainingPayoutUSD(ctx, ref)
		if err != nil {
			return closed(reasonProviderUnavailable), nil
		}
		ceiling = smallerUSD(ceiling, &remaining)
	}
	if ceiling != nil {
		option.MaxUSD = dollars(*ceiling)
	}
	return option, nil
}

// bankRefusal is the reason the router's refusal reads as on the options
// screen, and whether it is one: an error of ours is not.
func bankRefusal(err error) (string, bool) {
	var blocked *ramp.KycBlocked
	switch {
	case errors.Is(err, ramp.ErrTosRequired):
		return reasonTosRequired, true
	case errors.As(err, &blocked),
		errors.Is(err, ramp.ErrIntegrationRequired),
		errors.Is(err, ramp.ErrKycPending),
		errors.Is(err, ramp.ErrKycRejected),
		errors.Is(err, ramp.ErrRailNotEligible):
		return reasonKycRequired, true
	case errors.Is(err, ramp.ErrNoProviderAvailable):
		return reasonProviderUnavailable, true
	}
	return "", false
}

// dollars writes an amount with two decimals, always. A value that is not a
// number is left as written.
func dollars(amount string) string {
	value, ok := new(big.Rat).SetString(amount)
	if !ok {
		return amount
	}
	return value.FloatString(2)
}

// largerUSD is the larger of the app's floor and the rail's own, in dollars
// with two decimals.
func largerUSD(floor string, configured *string) string {
	if configured == nil {
		return dollars(floor)
	}
	a, aok := new(big.Rat).SetString(floor)
	b, bok := new(big.Rat).SetString(*configured)
	if !aok || !bok || a.Cmp(b) >= 0 {
		return dollars(floor)
	}
	return dollars(*configured)
}

// smallerUSD is the smaller of two optional ceilings; one that is absent, or
// not a number, binds nothing.
func smallerUSD(a, b *string) *string {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	x, xok := new(big.Rat).SetString(*a)
	y, yok := new(big.Rat).SetString(*b)
	switch {
	case !xok:
		return b
	case !yok, x.Cmp(y) <= 0:
		return a
	}
	return b
}
