package blindpay

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// limitsResponse is the slice of GetCustomerLimitsOut the adapter reads; the
// amounts are in cents of a dollar.
type limitsResponse struct {
	Limits struct {
		Payout struct {
			Daily   quoteNumber `json:"daily"`
			Monthly quoteNumber `json:"monthly"`
		} `json:"payout"`
	} `json:"limits"`
}

var _ ramp.PayoutLimitReader = (*Adapter)(nil)

// RemainingPayoutUSD answers the smaller of the customer's daily and monthly
// payout allowance, in dollars with two places.
func (a *Adapter) RemainingPayoutUSD(ctx context.Context, ref ramp.CustomerRef) (string, error) {
	if ref.ExternalID == "" {
		return "", ramp.ErrIntegrationRequired
	}
	client, err := a.client(ctx)
	if err != nil {
		return "", err
	}
	var raw json.RawMessage
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "limits/customers/" + url.PathEscape(ref.ExternalID)}, &raw); err != nil {
		return "", err
	}
	var out limitsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("blindpay: limits: decode: %w", ramp.ErrProviderFailed)
	}
	daily, monthly := out.Limits.Payout.Daily.rat, out.Limits.Payout.Monthly.rat
	if daily == nil || monthly == nil || daily.Sign() < 0 || monthly.Sign() < 0 {
		return "", fmt.Errorf("blindpay: limits: payout allowance missing or negative: %w", ramp.ErrProviderFailed)
	}
	smaller := daily
	if monthly.Cmp(daily) < 0 {
		smaller = monthly
	}
	return centsString(new(big.Rat).Set(smaller)), nil
}
