package bridge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// virtualAccountPageLimit bounds the listing read before a create. XIP opens
// one account per currency, so a page this size is the whole set.
const virtualAccountPageLimit = 100

// virtualAccountHistoryLimit bounds the history a deposit is looked up in:
// Bridge has no single-deposit read (measured), and the reconciler asks only
// about non-terminal, therefore recent, deposits.
const virtualAccountHistoryLimit = 100

// ProvisionVirtualAccount returns the customer's standing deposit account for
// the currency, creating one only when there is none.
//
// Read-then-create is mandatory here, unlike for external accounts: Bridge
// refuses a second registration of the same PIX key (400
// duplicate_external_account in sandbox, 422 in production), but does not
// deduplicate virtual accounts at all. Measured in the sandbox: a second POST
// under a new Idempotency-Key returned a second account with its own deposit
// code, and money paid into the untracked one has no order to attach to. The service refuses a concurrent create with
// ErrProvisioningInFlight; its lock does not span this call.
func (a *Adapter) ProvisionVirtualAccount(
	ctx context.Context,
	ref ramp.CustomerRef,
	req ramp.VirtualAccountRequest,
) (*ramp.VirtualAccount, error) {
	// A token Bridge cannot price could never settle a deposit.
	if err := refuseCurrency(req.Destination.Currency); err != nil {
		return nil, err
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	sourceCurrency := normalizeCurrency(req.SourceCurrency)
	if sourceCurrency == "" {
		return nil, errors.New("bridge: a virtual account needs a source currency to collect")
	}

	existing, err := a.findVirtualAccount(ctx, ref, sourceCurrency)
	if err != nil {
		return nil, ramp.NotSent(err)
	}
	if existing != nil {
		return existing, nil
	}

	body := virtualAccountRequest{
		Source: virtualAccountSource{Currency: sourceCurrency},
		Destination: virtualAccountDestination{
			PaymentRail: req.Destination.PaymentRail,
			Currency:    normalizeCurrency(req.Destination.Currency),
			Address:     req.Destination.Address,
		},
		DeveloperFeePercent: req.FeePercent,
	}

	resp, raw, err := ramp.Call[virtualAccountResponse](ctx, a.do, Request{
		Method:         http.MethodPost,
		Path:           virtualAccountsPath(ref.ExternalID),
		Body:           body,
		IdempotencyKey: provisionIdempotencyKey(ref, sourceCurrency, req.IdempotencyKey),
	})
	if err != nil {
		return nil, err
	}
	return accountFrom(resp, raw)
}

// GetVirtualAccount re-reads one account.
func (a *Adapter) GetVirtualAccount(
	ctx context.Context,
	ref ramp.CustomerRef,
	accountID string,
) (*ramp.VirtualAccount, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if accountID == "" {
		return nil, errors.New("bridge: get virtual account requires an account id")
	}

	resp, raw, err := ramp.Call[virtualAccountResponse](ctx, a.do, Request{
		Method: http.MethodGet,
		Path:   virtualAccountPath(ref.ExternalID, accountID),
	})
	if err != nil {
		return nil, err
	}
	return accountFrom(resp, raw)
}

// UpdateVirtualAccount changes the destination, the fee or the activation of an
// existing account, sending only what the caller named (see
// virtualAccountUpdateRequest). Activation is its own endpoint, so an update
// names either it or the rest, never both.
func (a *Adapter) UpdateVirtualAccount(
	ctx context.Context,
	ref ramp.CustomerRef,
	accountID string,
	req ramp.VirtualAccountUpdate,
) (*ramp.VirtualAccount, error) {
	if req.Destination != nil {
		if err := refuseCurrency(req.Destination.Currency); err != nil {
			return nil, err
		}
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if accountID == "" {
		return nil, errors.New("bridge: update virtual account requires an account id")
	}
	if req.Active != nil {
		if req.Destination != nil || req.FeePercent != "" {
			return nil, errors.New("bridge: activation cannot be combined with other virtual account changes")
		}
		return a.setVirtualAccountActive(ctx, ref, accountID, *req.Active)
	}

	body := virtualAccountUpdateRequest{DeveloperFeePercent: req.FeePercent}
	if req.Destination != nil {
		body.Destination = &virtualAccountDestination{
			PaymentRail: req.Destination.PaymentRail,
			Currency:    normalizeCurrency(req.Destination.Currency),
			Address:     req.Destination.Address,
		}
	}

	resp, raw, err := ramp.Call[virtualAccountResponse](ctx, a.do, Request{
		Method: http.MethodPut,
		Path:   virtualAccountPath(ref.ExternalID, accountID),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}
	return accountFrom(resp, raw)
}

// setVirtualAccountActive is POST …/activate or …/deactivate; production
// refuses `status` in the PUT body.
//
// Bridge requires an Idempotency-Key here, and each call gets a fresh one.
// Setting a status is idempotent by itself, so a retry needs no shared key,
// while a derived one would be replayed: deactivate, activate, deactivate under
// one key answers the first deactivation without performing the last, and
// Bridge refuses for good a key first used more than 24 hours earlier.
func (a *Adapter) setVirtualAccountActive(
	ctx context.Context,
	ref ramp.CustomerRef,
	accountID string,
	active bool,
) (*ramp.VirtualAccount, error) {
	action := "deactivate"
	if active {
		action = "activate"
	}
	resp, raw, err := ramp.Call[virtualAccountResponse](ctx, a.do, Request{
		Method:         http.MethodPost,
		Path:           virtualAccountPath(ref.ExternalID, accountID) + "/" + action,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		return nil, err
	}
	return accountFrom(resp, raw)
}

// GetDeposit re-reads one deposit out of the account's history, the rail's
// reconciliation path: /transfers/{id} 404s on a deposit id.
func (a *Adapter) GetDeposit(
	ctx context.Context,
	ref ramp.CustomerRef,
	accountID, depositID string,
) (*ramp.VirtualAccountDeposit, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if accountID == "" || depositID == "" {
		return nil, errors.New("bridge: get deposit requires a virtual account id and a deposit id")
	}

	var history virtualAccountList
	if err := a.do(ctx, Request{
		Method: http.MethodGet,
		Path:   virtualAccountPath(ref.ExternalID, accountID) + "/history",
		Query:  limitQuery(virtualAccountHistoryLimit),
	}, &history); err != nil {
		return nil, err
	}
	return depositFromHistory(history, accountID, depositID)
}

func virtualAccountsPath(customerID string) string {
	return customerPath(customerID) + "/virtual_accounts"
}

func virtualAccountPath(customerID, accountID string) string {
	return virtualAccountsPath(customerID) + "/" + accountID
}

func limitQuery(limit int) url.Values {
	return url.Values{"limit": {strconv.Itoa(limit)}}
}

// provisionIdempotencyKey prefers the caller's key and derives one otherwise:
// without a key, a retried timeout would create the second account
// ProvisionVirtualAccount exists to prevent.
func provisionIdempotencyKey(ref ramp.CustomerRef, sourceCurrency, supplied string) string {
	if supplied != "" {
		return supplied
	}
	return "va:" + ref.ExternalID + ":" + sourceCurrency
}

// findVirtualAccount returns the customer's reusable account for the currency,
// or nil when there is none.
//
// Only Bridge's raw `deactivated` is skipped: funds sent to one bounce, and
// deactivate-and-reprovision is supported. Matching on the canonical `active`
// would skip a state we do not recognise (it normalizes to inactive) and create
// the duplicate Bridge does not prevent. An account in an unknown state is a
// visible problem; a second account is a silent one.
func (a *Adapter) findVirtualAccount(
	ctx context.Context,
	ref ramp.CustomerRef,
	sourceCurrency string,
) (*ramp.VirtualAccount, error) {
	var list virtualAccountList
	if err := a.do(ctx, Request{
		Method: http.MethodGet,
		Path:   virtualAccountsPath(ref.ExternalID),
		Query:  limitQuery(virtualAccountPageLimit),
	}, &list); err != nil {
		return nil, err
	}

	for _, raw := range list.Data {
		var listed virtualAccountResponse
		if err := json.Unmarshal(raw, &listed); err != nil {
			return nil, fmt.Errorf("bridge: decode listed virtual account: %w", err)
		}
		if listed.Status == bridgeVirtualAccountDeactivated {
			continue
		}

		account, err := accountFrom(listed, raw)
		if err != nil {
			return nil, err
		}
		if account.SourceCurrency != sourceCurrency {
			continue
		}
		return account, nil
	}
	return nil, nil
}

// accountFrom normalizes a created, fetched or listed account. A listed element
// is what a create returns, so a reused account persists the same bytes.
func accountFrom(resp virtualAccountResponse, raw json.RawMessage) (*ramp.VirtualAccount, error) {
	if resp.ID == "" {
		return nil, errors.New("bridge: virtual account has no id")
	}
	return &ramp.VirtualAccount{
		ProviderAccountID:   resp.ID,
		Status:              virtualAccountStatusFromBridge(resp.Status),
		DepositInstructions: resp.SourceDepositInstructions,
		Destination: ramp.CryptoLeg{
			Currency:    resp.Destination.Currency,
			PaymentRail: resp.Destination.PaymentRail,
			Address:     resp.Destination.Address,
		},
		SourceCurrency: resp.sourceCurrency(),
		FeePercent:     resp.DeveloperFeePercent,
		Raw:            raw,
	}, nil
}

// virtualAccountStatusFromBridge reports anything but `activated` as inactive:
// claiming otherwise would show a payment target that bounces the money.
func virtualAccountStatusFromBridge(status string) string {
	if status == bridgeVirtualAccountActivated {
		return models.VirtualAccountStatusActive
	}
	return models.VirtualAccountStatusInactive
}

// depositFromHistory picks one deposit out of an account's activity list. A
// deposit is several activities sharing a `deposit_id`, and the latest by
// timestamp, not by position, describes it: nothing promises which end of the
// page is newest.
func depositFromHistory(
	history virtualAccountList,
	accountID, depositID string,
) (*ramp.VirtualAccountDeposit, error) {
	var (
		latest    virtualAccountActivity
		latestRaw json.RawMessage
		latestAt  *time.Time
		found     bool
		// The fiat leg, as read off the one activity with no receipt.
		fiatAmount, fiatCurrency string
	)

	for _, raw := range history.Data {
		var activity virtualAccountActivity
		if err := json.Unmarshal(raw, &activity); err != nil {
			return nil, fmt.Errorf("bridge: decode virtual account activity: %w", err)
		}
		if activity.DepositID != depositID {
			continue
		}
		if currency := activity.fiatCurrency(); currency != "" {
			fiatAmount, fiatCurrency = activity.Amount, currency
		}
		occurredAt := ramp.ParseTime(activity.CreatedAt)
		if !found || occurredAfter(occurredAt, latestAt) {
			latest, latestRaw, latestAt, found = activity, raw, occurredAt, true
		}
	}

	if !found {
		// Reporting an empty deposit would let the reconciler blank an order it
		// never saw.
		return nil, fmt.Errorf("bridge: deposit %s is not in the last %d activities of virtual account %s",
			depositID, virtualAccountHistoryLimit, accountID)
	}

	deposit := depositFromActivity(latest, latestRaw)
	deposit.ProviderAccountID = cmp.Or(latest.VirtualAccountID, accountID)

	// Only a history read sees the funds event beside the others, so only here
	// is the deposit reported in the currency the user sent; the fee comes off
	// that fiat, so it is quoted in it too.
	deposit.SourceAmount = cmp.Or(deposit.SourceAmount, fiatAmount)
	deposit.SourceCurrency, deposit.FeeCurrency = fiatCurrency, fiatCurrency
	return deposit, nil
}

// depositFromActivity normalizes one activity into the deposit it describes,
// for both the webhook and the history read, so the two paths cannot disagree
// on a status. It reports only what one activity can say.
func depositFromActivity(activity virtualAccountActivity, raw json.RawMessage) *ramp.VirtualAccountDeposit {
	// The amounts come off the receipt. `amount` switches legs between
	// activities ("100.0"/"usd" on funds_received, "97.5"/"usdc" after), and
	// is the fiat leg only while there is no receipt.
	sourceAmount := activity.Receipt.InitialAmount
	if activity.Receipt.isEmpty() {
		sourceAmount = activity.Amount
	}

	senderName, senderReference := activity.payer()
	return &ramp.VirtualAccountDeposit{
		DepositID:         activity.DepositID,
		ProviderAccountID: activity.VirtualAccountID,
		Status:            depositStatusFromBridge(activity.Type),
		SourceAmount:      sourceAmount,
		// SourceCurrency and FeeCurrency stay empty: after the first event
		// `currency` names the token and the receipt has no initial_currency,
		// so the processor reads the account's source_currency instead.
		//
		// Empty until a receipt exists, rather than a zero that reads as
		// "nothing was delivered".
		DestinationAmount: activity.Receipt.FinalAmount,
		FeeAmount:         cmp.Or(activity.DeveloperFeeAmount, activity.Receipt.DeveloperFee),
		ExchangeFeeAmount: cmp.Or(activity.ExchangeFeeAmount, activity.Receipt.ExchangeFee),
		TxHash:            cmp.Or(activity.Receipt.DestinationTxHash, activity.DestinationTxHash),
		// FailureReason stays empty: no real delivery has carried one.
		SenderName:      senderName,
		SenderReference: senderReference,
		PaymentRail:     bankPaymentRail(activity.sourceField("payment_rail")),
		OccurredAt:      ramp.ParseTime(activity.CreatedAt),
		Raw:             raw,
	}
}

// occurredAfter orders two activity timestamps, either of which may be missing;
// one with no readable time never displaces one that has it.
func occurredAfter(candidate, current *time.Time) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	return candidate.After(*current)
}

// The port lives outside ProviderAdapter, so only this line turns a missing
// method into a compile error rather than a rail silently reported unserved.
var _ ramp.VirtualAccountAdapter = (*Adapter)(nil)
