package blindpay

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// payoutIDPrefix tells a payout id from a payin's: the id decides the leg.
const payoutIDPrefix = "po_"

var _ ramp.BoundOrderReader = (*Adapter)(nil)

// GetBoundOrder reads a payout and answers it only if it is the one the
// binding describes. Any field that differs refuses the payout and applies
// nothing: the log carries the payout id, never a value.
func (a *Adapter) GetBoundOrder(ctx context.Context, ref ramp.CustomerRef, providerOrderID string, binding ramp.OrderBinding) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if len(providerOrderID) != quoteIDLen || !strings.HasPrefix(providerOrderID, payoutIDPrefix) || strings.ContainsAny(providerOrderID, "/.?#%\\") {
		return nil, fmt.Errorf("blindpay: payout id is malformed: %w", ramp.ErrBindingMismatch)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "payouts/" + providerOrderID}, &raw); err != nil {
		return nil, err
	}
	var payout payoutResponse
	if err := json.Unmarshal(raw, &payout); err != nil {
		return nil, fmt.Errorf("blindpay: payout: decode: %w", ramp.ErrProviderFailed)
	}
	if payout.ID != providerOrderID || !payoutMatches(payout, binding) {
		facades.Log().Errorf("ramp.attention: blindpay payout %s does not match its binding", providerOrderID)
		return nil, fmt.Errorf("blindpay: payout %s: %w", providerOrderID, ramp.ErrBindingMismatch)
	}

	return orderOfPayout(payout, raw), nil
}

// orderOfPayout normalizes a payout already held to its binding, flagging for
// an operator one that owes a refund or whose status is not known. The log
// carries the payout id, never a value.
func orderOfPayout(payout payoutResponse, raw json.RawMessage) *ramp.RampOrder {
	order, attention := payoutOrder(payout, raw)
	if attention {
		facades.Log().Errorf("ramp.attention: blindpay payout %s %s", payout.ID, order.Status)
	}
	if !payoutStatusKnown(payout.Status) {
		facades.Log().Errorf("ramp.attention: blindpay payout %s has a status it does not know", payout.ID)
	}
	return order
}

// payoutMatches holds every field of the binding against the payout. An empty
// binding field never matches: it would equal a payout that omits it.
func payoutMatches(p payoutResponse, b ramp.OrderBinding) bool {
	pairs := [][2]string{
		{p.CustomerID, b.ProviderCustomerID},
		{p.QuoteID, b.QuoteID},
		{p.BankAccountID, b.BankAccountID},
		{p.Network, b.Network},
		{p.Token, b.Token},
		{p.SenderWalletAddress, b.SenderWallet},
	}
	for _, pair := range pairs {
		if pair[1] == "" || pair[0] != pair[1] {
			return false
		}
	}
	return senderAmountMatches(p.SenderAmount, b.SenderAmount)
}

// senderAmountMatches compares by value: the binding spells the amount in
// units as the quote did, the payout answers it in cents.
func senderAmountMatches(sent quoteNumber, bound string) bool {
	if sent.rat == nil || !decimalAmount.MatchString(bound) {
		return false
	}
	want, ok := new(big.Rat).SetString(bound)
	if !ok {
		return false
	}
	return new(big.Rat).Quo(sent.rat, big.NewRat(100, 1)).Cmp(want) == 0
}

// payoutOrder normalizes a payout however it arrived. Its amounts are what
// BlindPay answered; one it left out is left out here too.
func payoutOrder(p payoutResponse, raw json.RawMessage) (*ramp.RampOrder, bool) {
	amounts := map[string]string{
		"source_amount":      payinAmount(p.SenderAmount),
		"destination_amount": payinAmount(p.ReceiverAmount),
	}
	maps.DeleteFunc(amounts, func(_, value string) bool { return value == "" })
	status, attention := payoutStatus(p)
	return &ramp.RampOrder{
		ProviderOrderID: p.ID,
		Status:          status,
		Amounts:         amounts,
		TxHash:          payoutTxHash(p),
		FailureReason:   payoutFailureReason(p),
		Raw:             raw,
	}, attention
}
