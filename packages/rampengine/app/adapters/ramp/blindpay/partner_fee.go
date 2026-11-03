package blindpay

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"regexp"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// maxFeeBps is BlindPay's ceiling on a partner percentage fee (99%).
const maxFeeBps = 9900

// feePercent is the only shape a fee percent may take: digits with an
// optional fraction. It rules out signs and exponents.
var feePercent = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// partnerFeeRequest is the create body; the field order is fixed.
type partnerFeeRequest struct {
	Name                string `json:"name"`
	PayinPercentageFee  int    `json:"payin_percentage_fee"`
	PayinFlatFee        int    `json:"payin_flat_fee"`
	PayoutPercentageFee int    `json:"payout_percentage_fee"`
	PayoutFlatFee       int    `json:"payout_flat_fee"`
}

// partnerFeeResponse is a partner fee profile as BlindPay lists and creates
// it, with the fees it charges.
type partnerFeeResponse struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	PayinPercentageFee  quoteNumber `json:"payin_percentage_fee"`
	PayinFlatFee        quoteNumber `json:"payin_flat_fee"`
	PayoutPercentageFee quoteNumber `json:"payout_percentage_fee"`
	PayoutFlatFee       quoteNumber `json:"payout_flat_fee"`
}

// charges reports whether the profile charges exactly payinBps and payoutBps
// with no flat fee, the profile partnerFeeID asked for. A fee left out is not
// zero: it is a profile whose charge is unknown.
func (r partnerFeeResponse) charges(payinBps, payoutBps int) bool {
	is := func(n quoteNumber, want int) bool {
		return n.rat != nil && n.rat.Cmp(big.NewRat(int64(want), 1)) == 0
	}
	return is(r.PayinPercentageFee, payinBps) && is(r.PayinFlatFee, 0) &&
		is(r.PayoutPercentageFee, payoutBps) && is(r.PayoutFlatFee, 0)
}

// feeBps reads XIP's fee percent ("1.55") as BlindPay basis points (155),
// rounding down so the customer is never charged more than configured.
func feeBps(percent string) (int, error) {
	if percent == "" {
		return 0, nil
	}
	if !feePercent.MatchString(percent) {
		return 0, fmt.Errorf("blindpay: fee percent %q: %w", percent, ramp.ErrRequestNotSent)
	}
	rat, ok := new(big.Rat).SetString(percent)
	if !ok {
		return 0, fmt.Errorf("blindpay: fee percent %q: %w", percent, ramp.ErrRequestNotSent)
	}
	rat.Mul(rat, big.NewRat(100, 1))
	bps := new(big.Int).Quo(rat.Num(), rat.Denom())
	if !bps.IsInt64() || bps.Int64() > maxFeeBps {
		return 0, fmt.Errorf("blindpay: fee percent %q above 99%%: %w", percent, ramp.ErrRequestNotSent)
	}
	return int(bps.Int64()), nil
}

func partnerFeeName(payinBps, payoutBps int) string {
	return fmt.Sprintf("xip-fee-in%d-out%d", payinBps, payoutBps)
}

// partnerFeeID answers the id of the partner fee profile charging payinBps on
// payins and payoutBps on payouts, finding or creating it; ("", nil) when both
// are zero.
//
// A profile is matched by its name and by the fees it charges. The name is
// only a convention, and anyone with dashboard access can give another
// profile the same one: a profile under XIP's name that charges something
// else is refused and flagged, never used.
func (a *Adapter) partnerFeeID(ctx context.Context, client *Client, payinBps, payoutBps int) (string, error) {
	if payinBps == 0 && payoutBps == 0 {
		return "", nil
	}
	name := partnerFeeName(payinBps, payoutBps)
	key := client.InstanceID() + "/" + name
	if id, ok := a.partnerFees.Load(key); ok {
		return id.(string), nil
	}
	return shareFlight(ctx, &a.feeFlight, key, func(flight context.Context) (string, error) {
		return a.findOrCreatePartnerFee(flight, client, key, name, payinBps, payoutBps)
	})
}

// findOrCreatePartnerFee is partnerFeeID's provider half, run once per key at
// a time (feeFlight).
func (a *Adapter) findOrCreatePartnerFee(
	ctx context.Context, client *Client, key, name string, payinBps, payoutBps int,
) (string, error) {
	// A flight that just finished may have stored it.
	if id, ok := a.partnerFees.Load(key); ok {
		return id.(string), nil
	}
	var list []partnerFeeResponse
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "partner-fees"}, &list); err != nil {
		return "", err
	}
	impostor := ""
	for _, item := range list {
		if item.Name != name || item.ID == "" {
			continue
		}
		if item.charges(payinBps, payoutBps) {
			a.partnerFees.Store(key, item.ID)
			return item.ID, nil
		}
		impostor = item.ID
	}
	if impostor != "" {
		facades.Log().Errorf("ramp.attention: blindpay partner fee %s is named %s but charges other fees", impostor, name)
		return "", fmt.Errorf("blindpay: partner fee %s charges other fees: %w", name, ramp.ErrProviderFailed)
	}
	var created partnerFeeResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "partner-fees", IdempotencyKey: a.createKey(name),
		Body: partnerFeeRequest{Name: name, PayinPercentageFee: payinBps, PayoutPercentageFee: payoutBps},
	}, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("blindpay: partner fee create answered no id: %w", ramp.ErrProviderFailed)
	}
	if !created.charges(payinBps, payoutBps) {
		facades.Log().Errorf("ramp.attention: blindpay partner fee %s was created charging other fees than %s", created.ID, name)
		return "", fmt.Errorf("blindpay: partner fee %s charges other fees: %w", name, ramp.ErrProviderFailed)
	}
	a.partnerFees.Store(key, created.ID)
	return created.ID, nil
}

// forgetPartnerFee drops a cached id the provider no longer knows.
func (a *Adapter) forgetPartnerFee(instanceID string, payinBps, payoutBps int) {
	a.partnerFees.Delete(instanceID + "/" + partnerFeeName(payinBps, payoutBps))
}
