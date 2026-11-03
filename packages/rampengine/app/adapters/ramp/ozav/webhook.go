package ozav

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// ErrInvalidSignature is every reason a delivery was not accepted. The reply is
// the same for all of them, so a prober learns nothing; only a secret this side
// cannot use wraps it with the cause, for the log.
var ErrInvalidSignature = errors.New("ozav: webhook signature is not valid")

// WebhookSignatureHeader carries "t=<unix>,v1=<hex>".
const WebhookSignatureHeader = "X-OZAV-Signature"

// webhookTolerance bounds how far a delivery's timestamp may be from now. The
// timestamp is signed, so this is the replay defence: a captured delivery is
// signed correctly forever and only the clock refuses it. Five minutes is
// Ozav's recommendation, and every retry is signed afresh.
const webhookTolerance = 5 * time.Minute

// The event types this adapter models.
const (
	eventTransferUpdated     = "transfer.updated"
	eventTransferCompleted   = "transfer.completed"
	eventTransferFailed      = "transfer.failed"
	eventCustomerCreated     = "customer.created"
	eventCustomerUpdated     = "customer.updated"
	eventVerificationCreated = "verification.created"
	eventVerificationUpdated = "verification.updated"
	eventDepositCredited     = "deposit.credited"
)

// SecretFactory resolves the webhook signing secret per call, like
// ClientFactory.
type SecretFactory func(ctx context.Context) (string, error)

// webhookEnvelope is Ozav's delivery; `data` is the resource in the shape its
// GET returns.
type webhookEnvelope struct {
	ID        string          `json:"id"`
	Object    string          `json:"object"`
	Type      string          `json:"type"`
	CreatedAt string          `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// ParseWebhook verifies a delivery over the raw body before anything decodes
// it, so a forged payload never reaches a parser.
func (a *Adapter) ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (*ramp.WebhookResult, error) {
	if err := a.verifyWebhook(ctx, headers, body); err != nil {
		return nil, err
	}
	return a.DecodeWebhook(ctx, body)
}

// DecodeWebhook maps a payload verified at ingest; reprocessing reads bodies
// back from `webhook_events`, where the headers are gone. That is also why the
// event id comes from the envelope rather than X-OZAV-Event-ID: dedupe must
// see the same id on both paths.
func (a *Adapter) DecodeWebhook(_ context.Context, body []byte) (*ramp.WebhookResult, error) {
	var envelope webhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("ozav: decode webhook: %w", err)
	}

	result := &ramp.WebhookResult{
		Kind:       ramp.WebhookKindUnknown,
		EventID:    envelope.ID,
		EventType:  envelope.Type,
		OccurredAt: ramp.ParseTime(envelope.CreatedAt),
		Raw:        body,
	}

	switch envelope.Type {
	case eventTransferUpdated, eventTransferCompleted, eventTransferFailed:
		transfer, err := decodeTransferEventData(envelope.Data)
		if err != nil {
			return nil, err
		}
		order := orderFrom(transfer, nil)
		result.Kind = ramp.WebhookKindRamp
		result.ProviderOrderID = order.ProviderOrderID
		result.ProviderQuoteID = transfer.QuoteID
		result.Status = order.Status
		result.TxHash = order.TxHash
		result.DeliveryFailed = order.DeliveryFailed
		result.FailureReason = order.FailureReason
		result.SourceAmount = order.Amounts["source_amount"]
		result.DestinationAmount = order.Amounts["destination_amount"]
		result.ExchangeRate = order.Amounts["exchange_rate"]

	case eventCustomerCreated, eventCustomerUpdated:
		customer, err := decodeCustomerEventData(envelope.Data)
		if err != nil {
			return nil, err
		}
		result.Kind = ramp.WebhookKindKyc
		result.ExternalCustomerID = customer.ID
		result.Status = customerStatus(customer)
		result.Endorsements = []ramp.Endorsement{
			synthesizeEndorsement(customer.Status, customer.Verification.Mode),
		}
		// What made the status incomplete is what the user has to send, and
		// pending_steps is derived from it: a status without it reads as a
		// review in progress. The documents listing GetKyc also reads is not in
		// the event; the customer's own documents_due is compliance's request.
		result.Requirements = requirementsFrom(customer.Requirements)
		result.ReportsRequirements = true
		// The event carries no still_required, so a document named only
		// there can make this warn while the next sync names it.
		customer.logUnnamedRequest(nil)
		rejection := customer.loggedRejection()
		result.RejectionCategories = rejection.categories
		result.RejectionDetails = rejection.details

	case eventVerificationCreated, eventVerificationUpdated:
		var session verificationSession
		if err := json.Unmarshal(envelope.Data, &session); err != nil {
			return nil, fmt.Errorf("ozav: decode verification event: %w", err)
		}
		// Keyed on the customer, with no status: a session's status
		// ("in_progress", "expired") is not the customer's posture, and the
		// customer event that follows carries that.
		result.Kind = ramp.WebhookKindKyc
		result.ExternalCustomerID = session.CustomerID

	case eventDepositCredited:
		deposit, err := decodeDepositEventData(envelope.Data)
		if err != nil {
			return nil, err
		}
		result.Kind = ramp.WebhookKindManagedWalletDeposit
		result.ProviderAccountID = deposit.WalletID
		result.TxHash = deposit.TransactionHash
		result.ManagedWalletDeposit = &ramp.ManagedWalletDeposit{
			DepositID: deposit.ID,
			WalletID:  deposit.WalletID,
			Amount:    deposit.Amount,
			Currency:  deposit.Currency,
			Network:   deposit.Network,
			TxHash:    deposit.TransactionHash,
		}
	}

	return result, nil
}

type depositEvent struct {
	ID              string `json:"id"`
	WalletID        string `json:"wallet_id"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	Network         string `json:"network"`
	TransactionHash string `json:"transaction_hash"`
}

func decodeDepositEventData(raw json.RawMessage) (depositEvent, error) {
	var wrapped struct {
		Deposit depositEvent `json:"deposit"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Deposit.WalletID != "" {
		return wrapped.Deposit, nil
	}
	var deposit depositEvent
	if err := json.Unmarshal(raw, &deposit); err != nil {
		return depositEvent{}, fmt.Errorf("ozav: decode deposit event: %w", err)
	}
	if deposit.WalletID == "" {
		return depositEvent{}, fmt.Errorf("ozav: deposit event missing wallet_id: %w", ramp.ErrProviderFailed)
	}
	return deposit, nil
}

// decodeCustomerEventData reads a customer from webhook `data`. Live deliveries
// wrap the resource as {"customer":{…}}; tests and older captures use the
// customer object at the top level.
func decodeCustomerEventData(raw json.RawMessage) (customerResponse, error) {
	var wrapped struct {
		Customer customerResponse `json:"customer"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Customer.ID != "" {
		return wrapped.Customer, nil
	}
	var customer customerResponse
	if err := json.Unmarshal(raw, &customer); err != nil {
		return customerResponse{}, fmt.Errorf("ozav: decode customer event: %w", err)
	}
	return customer, nil
}

// decodeTransferEventData reads a transfer from webhook `data`. Live deliveries
// wrap the resource as {"transfer":{…}}; older captures and tests use the
// transfer object at the top level. A transfer it cannot read, or one without
// an id, is an error: read as an empty transfer, the event names no order and
// is stamped processed with nothing applied.
func decodeTransferEventData(raw json.RawMessage) (transferResponse, error) {
	var wrapped struct {
		Transfer *json.RawMessage `json:"transfer"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return transferResponse{}, fmt.Errorf("ozav: decode transfer event: %w", err)
	}
	if wrapped.Transfer != nil {
		raw = *wrapped.Transfer
	}
	var transfer transferResponse
	if err := json.Unmarshal(raw, &transfer); err != nil {
		return transferResponse{}, fmt.Errorf("ozav: decode transfer event: %w", err)
	}
	if transfer.ID == "" {
		return transferResponse{}, fmt.Errorf("ozav: transfer event missing id: %w", ramp.ErrProviderFailed)
	}
	return transfer, nil
}

// verifyWebhook checks the delivery's HMAC over the exact bytes received.
func (a *Adapter) verifyWebhook(ctx context.Context, headers map[string]string, body []byte) error {
	secret, err := a.webhookSecret(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, err)
	}

	timestamp, signature, err := parseOzavSignature(ramp.HeaderValue(headers, WebhookSignatureHeader))
	if err != nil {
		return err
	}
	if skew := time.Since(timestamp); skew > webhookTolerance || skew < -webhookTolerance {
		return ErrInvalidSignature
	}

	// The secret is used as issued: `ozav_whsec_` is part of the key, not a
	// label. The signed material is "<t>." and the RAW body, never a
	// re-encoding; hash.Hash's Write never returns an error.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10) + "."))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return ErrInvalidSignature
	}
	return nil
}

// webhookSecret resolves the signing secret. Its errors say what is wrong with
// the configuration, never the secret, and an unconfigured secret refuses
// every delivery rather than accepting them.
func (a *Adapter) webhookSecret(ctx context.Context) (string, error) {
	if a.webhookSecrets == nil {
		return "", errors.New("no webhook secret resolver")
	}
	secret, err := a.webhookSecrets(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve webhook secret: %w", err)
	}
	if secret == "" {
		return "", errors.New("webhook secret not configured")
	}
	return secret, nil
}

// parseOzavSignature reads "t=<unix seconds>,v1=<hex>", finding v1 by name so
// Ozav can add members, as its guidance says.
func parseOzavSignature(header string) (time.Time, []byte, error) {
	var stamp, digest string
	for _, part := range strings.Split(header, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch name {
		case "t":
			stamp = value
		case "v1":
			digest = value
		}
	}
	if stamp == "" || digest == "" {
		return time.Time{}, nil, ErrInvalidSignature
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return time.Time{}, nil, ErrInvalidSignature
	}
	signature, err := hex.DecodeString(digest)
	if err != nil {
		return time.Time{}, nil, ErrInvalidSignature
	}
	return time.Unix(seconds, 0), signature, nil
}
