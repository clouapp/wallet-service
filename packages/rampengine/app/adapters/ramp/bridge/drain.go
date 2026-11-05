package bridge

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// drainHistoryLimit bounds the history a drain is looked up in. The query
// already filters by drain_id; the limit only keeps a filter Bridge ignores
// from returning the whole history.
const drainHistoryLimit = 10

// GetDrain re-reads one drain out of the address's history: the
// reconciliation path of a USD payout, as GetDeposit is of a virtual account
// deposit.
func (a *Adapter) GetDrain(
	ctx context.Context,
	ref ramp.CustomerRef,
	addressID, drainID string,
) (*ramp.LiquidationDrain, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if addressID == "" || drainID == "" {
		return nil, errors.New("bridge: get drain requires a liquidation address id and a drain id")
	}

	var history json.RawMessage
	if err := a.do(ctx, Request{
		Method: http.MethodGet,
		Path:   liquidationAddressPath(ref.ExternalID, addressID) + "/drains",
		Query:  url.Values{"drain_id": {drainID}, "limit": {strconv.Itoa(drainHistoryLimit)}},
	}, &history); err != nil {
		return nil, err
	}

	items, err := drainsFrom(history)
	if err != nil {
		return nil, err
	}
	for _, raw := range items {
		var drain drainResponse
		if err := json.Unmarshal(raw, &drain); err != nil {
			return nil, fmt.Errorf("bridge: decode drain: %w", err)
		}
		if drain.ID != drainID {
			continue
		}
		found := drainFrom(drain, raw)
		found.ProviderAddressID = cmp.Or(found.ProviderAddressID, addressID)
		return found, nil
	}
	return nil, fmt.Errorf("bridge: drain %s is not in the history of liquidation address %s", drainID, addressID)
}

// SimulateLiquidationDeposit asks the sandbox to pretend the user sent crypto
// to the address, and returns the drain it opened. The key is required: a
// retried timeout without one would simulate a second deposit.
func (a *Adapter) SimulateLiquidationDeposit(
	ctx context.Context,
	ref ramp.CustomerRef,
	addressID string,
	req ramp.SimulatedDeposit,
) (string, error) {
	if ref.ExternalID == "" {
		return "", ramp.ErrIntegrationRequired
	}
	if addressID == "" || req.IdempotencyKey == "" {
		return "", errors.New("bridge: simulate deposit requires a liquidation address id and an idempotency key")
	}

	resp, _, err := ramp.Call[simulateDepositResponse](ctx, a.do, Request{
		Method: http.MethodPost,
		Path:   liquidationAddressPath(ref.ExternalID, addressID) + "/simulate_deposit",
		Body: map[string]string{
			"currency": normalizeCurrency(req.Currency),
			"amount":   req.Amount,
		},
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return "", err
	}
	if resp.DrainID == "" && resp.Success != nil && !*resp.Success {
		// The sandbox's refusal: "Deposit was below minimum amount or failed
		// validation. No drain was created." Its floor (about US$ 1) is not in
		// the answer. Its "no drain" is not always true either — against a
		// deactivated external account it said so and opened one — so nothing
		// is recorded here: a drain that does exist reaches the order by the
		// webhook, the path every real payout takes.
		return "", fmt.Errorf("bridge: simulated deposit refused: %w", ramp.ErrAmountBelowMinimum)
	}
	if resp.DrainID == "" {
		return "", errors.New("bridge: simulated deposit opened no drain")
	}
	return resp.DrainID, nil
}

// drainsFrom reads the history either as {data: [...]} or as a bare array:
// the docs show both.
func drainsFrom(history json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(history)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("bridge: decode drains: %w", err)
		}
		return items, nil
	}
	var list drainList
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("bridge: decode drains: %w", err)
	}
	return list.Data, nil
}

// drainFrom normalizes a drain, for both the webhook and the history read, so
// the two paths cannot disagree on a status. Every amount is read, never
// computed: the receipt is what settles.
func drainFrom(drain drainResponse, raw json.RawMessage) *ramp.LiquidationDrain {
	return &ramp.LiquidationDrain{
		DrainID:           drain.ID,
		ProviderAddressID: drain.LiquidationAddressID,
		Status:            drainStatusFromBridge(drain.State),
		Returning:         drainReturning(drain.State),
		SourceAmount:      cmp.Or(drain.Receipt.InitialAmount, drain.Amount),
		SourceCurrency:    normalizeCurrency(drain.Currency),
		// Empty until a receipt exists, rather than a zero that reads as
		// "nothing was sent".
		DestinationAmount: drain.Receipt.OutgoingAmount,
		FeeAmount:         drain.Receipt.DeveloperFee,
		PaymentRail:       bankPaymentRail(drain.Destination.PaymentRail),
		DepositTxHash:     drain.DepositTxHash,
		FromAddress:       drain.FromAddress,
		BankReference:     cmp.Or(drain.Destination.IMAD, drain.Destination.TraceNumber),
		FailureReason:     drain.ReturnDetails.Reason,
		OccurredAt:        ramp.ParseTime(cmp.Or(drain.UpdatedAt, drain.CreatedAt)),
		Raw:               raw,
	}
}

// drainStatusFromBridge is spec D11. A drain on its way back (returned,
// refund_in_flight) is still in flight: the money is not with the user yet.
func drainStatusFromBridge(state string) string {
	switch state {
	case "awaiting_funds":
		return models.TxStatusAwaitingFunds
	case "funds_received", "payment_validated":
		return models.TxStatusFundsReceived
	case "in_review", "payment_submitted", "returned", "refund_in_flight",
		"kyc_required", "kyc_in_review", "underfunded":
		return models.TxStatusProcessing
	case "payment_processed":
		return models.TxStatusCompleted
	case "refunded":
		return models.TxStatusRefunded
	case "canceled", "error", "undeliverable", "refund_failed", "missing_return_policy":
		return models.TxStatusFailed
	default:
		// Same rule as transferStatusFromBridge.
		return models.TxStatusProcessing
	}
}

// drainReturning reports a drain the bank sent back after Bridge paid it out:
// returned and refund_in_flight while the money travels back, refund_failed
// when it could not be returned. Each can arrive after payment_processed.
func drainReturning(state string) bool {
	switch state {
	case "returned", "refund_in_flight", "refund_failed":
		return true
	}
	return false
}

// The port lives outside ProviderAdapter, so only this line turns a missing
// method into a compile error rather than a rail silently reported unserved.
var _ ramp.LiquidationAddressAdapter = (*Adapter)(nil)
