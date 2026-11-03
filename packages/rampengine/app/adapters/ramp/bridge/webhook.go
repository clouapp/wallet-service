package bridge

import (
	"cmp"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrInvalidSignature is every reason a delivery was not accepted. The reply is
// the same for all of them, so a prober learns nothing; only a key this side
// cannot use wraps it with the cause, for the log.
var ErrInvalidSignature = errors.New("bridge: webhook signature is not valid")

// WebhookSignatureHeader carries the timestamp and the signature.
const WebhookSignatureHeader = "X-Webhook-Signature"

// webhookTolerance bounds how far a delivery's timestamp may be from now. The
// timestamp is signed, so this is the replay defence: a captured delivery is
// signed correctly forever and only the clock refuses it. Ten minutes is
// Bridge's own guidance, and every retry carries a fresh timestamp.
const webhookTolerance = 10 * time.Minute

// Bridge's event categories.
const (
	eventCategoryTransfer = "transfer"
	eventCategoryCustomer = "customer"
	// Measured on the captured deliveries in testdata/va_activity_*.json.
	eventCategoryVirtualAccount = "virtual_account.activity"
	// A drain of a liquidation address: a USD payout on its way to the bank.
	eventCategoryLiquidationDrain = "liquidation_address.drain"
	// A customer's external (bank) account was created, updated or removed.
	eventCategoryExternalAccount = "external_account"
)

// The activity kinds the decoder classifies on. Every other kind is a stage of
// a deposit, named by depositStatusFromBridge.
const (
	activityMicrodeposit  = "microdeposit"
	activityAccountUpdate = "account_update"
	activityActivation    = "activation"
	activityDeactivation  = "deactivation"
)

// SecretFactory resolves the webhook verification key per call, like
// ClientFactory.
type SecretFactory func(ctx context.Context) (string, error)

// webhookEnvelope is Bridge's delivery.
type webhookEnvelope struct {
	EventID       string          `json:"event_id"`
	EventCategory string          `json:"event_category"`
	EventType     string          `json:"event_type"`
	EventObjectID string          `json:"event_object_id"`
	EventCreated  string          `json:"event_created_at"`
	EventObject   json.RawMessage `json:"event_object"`
}

// ParseWebhook verifies a delivery over the raw body before anything decodes
// it, so a forged payload never reaches a parser.
func (a *Adapter) ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (*ramp.WebhookResult, error) {
	if err := a.verifyWebhook(ctx, headers, body); err != nil {
		return nil, err
	}
	return a.DecodeWebhook(ctx, body)
}

// DecodeWebhook maps a payload verified at ingest. Reprocessing reads bodies
// back out of `webhook_events`, where the signature headers no longer exist.
func (a *Adapter) DecodeWebhook(_ context.Context, body []byte) (*ramp.WebhookResult, error) {
	var envelope webhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("bridge: decode webhook: %w", err)
	}

	result := &ramp.WebhookResult{
		Kind:       ramp.WebhookKindUnknown,
		EventID:    envelope.EventID,
		EventType:  envelope.EventType,
		OccurredAt: ramp.ParseTime(envelope.EventCreated),
		Raw:        body,
	}

	switch envelope.EventCategory {
	case eventCategoryTransfer:
		var transfer transferResponse
		if err := json.Unmarshal(envelope.EventObject, &transfer); err != nil {
			return nil, fmt.Errorf("bridge: decode transfer event: %w", err)
		}
		result.Kind = ramp.WebhookKindRamp
		result.ProviderOrderID = cmp.Or(envelope.EventObjectID, transfer.ID)
		result.Status = transferStatusFromBridge(transfer.State)
		result.TxHash = transfer.Receipt.DestinationTxHash
		result.ExchangeRate = transfer.Receipt.ExchangeRate
		result.ExchangeFee = transfer.Receipt.ExchangeFee
		result.SourceAmount = cmp.Or(transfer.Receipt.InitialAmount, transfer.Amount)
		result.DestinationAmount = transfer.Receipt.FinalAmount
	case eventCategoryCustomer:
		var customer customerResponse
		if err := json.Unmarshal(envelope.EventObject, &customer); err != nil {
			return nil, fmt.Errorf("bridge: decode customer event: %w", err)
		}
		result.Kind = ramp.WebhookKindKyc
		result.ExternalCustomerID = cmp.Or(envelope.EventObjectID, customer.ID)
		result.Status = kycStatusFromBridge(customer.Status)
		result.Endorsements = customer.endorsements()
		result.Requirements = customer.requirements()
		result.RejectionReasons = customer.rejectionMessages()
		result.ReportsRequirements = true
		rejection := customer.loggedRejection()
		result.RejectionCategories = rejection.categories
		result.RejectionDetails = rejection.details
	case eventCategoryVirtualAccount:
		var activity virtualAccountActivity
		if err := json.Unmarshal(envelope.EventObject, &activity); err != nil {
			return nil, fmt.Errorf("bridge: decode virtual account event: %w", err)
		}
		// `event_object_id` is the activity's own id (measured), neither the
		// deposit's nor the account's: keying on it would turn the four
		// activities of one deposit into four orders.
		result.ExternalCustomerID = activity.CustomerID
		result.ProviderAccountID = activity.VirtualAccountID
		// The kind comes from event_object.type: the envelope's event_type is
		// "virtual_account.activity.created" for every activity (measured).
		switch activity.Type {
		case activityMicrodeposit:
			// Bridge documents these funds as never onramped; a transaction
			// would show the user money that does not exist.
			result.Kind = ramp.WebhookKindUnknown
		case activityAccountUpdate, activityActivation, activityDeactivation:
			result.Kind = ramp.WebhookKindVirtualAccount
			result.Account = accountFromActivity(activity, envelope.EventObject)
		default:
			// A kind Bridge adds later is still money in the account, kept in
			// flight by depositStatusFromBridge.
			result.Kind = ramp.WebhookKindVirtualAccountDeposit
			result.Deposit = depositFromActivity(activity, envelope.EventObject)
		}
		// Qualified with the real kind so `webhook_events` and the lag metric
		// tell a funds_received from a payment_processed. Dedup keys on
		// (provider, event_id), never on this.
		if activity.Type != "" {
			result.EventType = eventCategoryVirtualAccount + "." + activity.Type
		}
	case eventCategoryLiquidationDrain:
		var drain drainResponse
		if err := json.Unmarshal(envelope.EventObject, &drain); err != nil {
			return nil, fmt.Errorf("bridge: decode liquidation address drain: %w", err)
		}
		// The drain names its address, not an order: like a virtual account
		// deposit, its first event is what creates the transaction, and the
		// address is the join back to the user.
		result.Kind = ramp.WebhookKindLiquidationDrain
		result.ExternalCustomerID = drain.CustomerID
		result.ProviderAccountID = drain.LiquidationAddressID
		result.Drain = drainFrom(drain, envelope.EventObject)
	case eventCategoryExternalAccount:
		var account struct {
			CustomerID string `json:"customer_id"`
		}
		if err := json.Unmarshal(envelope.EventObject, &account); err != nil {
			return nil, fmt.Errorf("bridge: decode external account event: %w", err)
		}
		// Only the customer matters: the account itself is this side's
		// record already. What changes with it is the customer's
		// requirements_due, which the processor re-reads.
		result.Kind = ramp.WebhookKindExternalAccount
		result.ExternalCustomerID = account.CustomerID
	}
	// An unmodelled category stays unknown: authentic, recorded and
	// acknowledged, so the provider does not retry it.
	return result, nil
}

// accountFromActivity describes what an account-level activity changed, not a
// snapshot: an empty field means the event says nothing about it, and the
// caller writes only what is set.
func accountFromActivity(activity virtualAccountActivity, raw json.RawMessage) *ramp.VirtualAccount {
	account := &ramp.VirtualAccount{
		ProviderAccountID: activity.VirtualAccountID,
		Destination: ramp.CryptoLeg{
			PaymentRail: activity.DestinationPaymentRail,
			Address:     activity.updatedValue("destination_address"),
		},
		FeePercent: activity.updatedValue("developer_fee_percent"),
		Raw:        raw,
	}
	// Only activation and deactivation set a status: asserting `active` on an
	// account_update would revive an account deactivated on purpose, and
	// deactivation is this rail's containment tool.
	switch activity.Type {
	case activityActivation:
		account.Status = models.VirtualAccountStatusActive
	case activityDeactivation:
		account.Status = models.VirtualAccountStatusInactive
	}
	return account
}

// verifyWebhook checks RSA-SHA256 over "<timestamp>.<body>" against the PEM
// key, failing closed: every uncertainty costs a retry, never a forgery.
func (a *Adapter) verifyWebhook(ctx context.Context, headers map[string]string, body []byte) error {
	publicKey, err := a.webhookKey(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, err)
	}

	timestamp, signature, err := parseSignatureHeader(ramp.HeaderValue(headers, WebhookSignatureHeader))
	if err != nil {
		return err
	}
	if skew := time.Since(timestamp); skew > webhookTolerance || skew < -webhookTolerance {
		return ErrInvalidSignature
	}

	signed := strconv.FormatInt(timestamp.UnixMilli(), 10) + "." + string(body)
	digest := sha256.Sum256([]byte(signed))
	if err := verifyBridgePKCS1v15(publicKey, digest[:], signature); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

// verifyBridgePKCS1v15 checks RSA-SHA256 over the SHA256 digest of
// "<timestamp>.<body>". Bridge's reference verifiers (Node, Python) feed the
// digest into RSA-SHA256, which hashes again — so live deliveries are signed
// over SHA256(digest). The stub and unit tests use a single hash, which the
// first attempt accepts.
func verifyBridgePKCS1v15(publicKey *rsa.PublicKey, digest, signature []byte) error {
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest, signature); err == nil {
		return nil
	}
	nested := sha256.Sum256(digest)
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, nested[:], signature)
}

// parseSignatureHeader reads Bridge's "t=<unix millis>,v0=<base64>" form.
func parseSignatureHeader(header string) (time.Time, []byte, error) {
	if header == "" {
		return time.Time{}, nil, ErrInvalidSignature
	}

	var rawTimestamp, rawSignature string
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch key {
		case "t":
			rawTimestamp = value
		case "v0":
			rawSignature = value
		}
	}
	if rawTimestamp == "" || rawSignature == "" {
		return time.Time{}, nil, ErrInvalidSignature
	}

	millis, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return time.Time{}, nil, ErrInvalidSignature
	}
	signature, err := base64.StdEncoding.DecodeString(rawSignature)
	if err != nil {
		return time.Time{}, nil, ErrInvalidSignature
	}
	return time.UnixMilli(millis), signature, nil
}

// webhookKey resolves and parses the verification key. Its errors name what is
// wrong with the configuration, never the key.
func (a *Adapter) webhookKey(ctx context.Context) (*rsa.PublicKey, error) {
	if a.webhookSecrets == nil {
		return nil, errors.New("no webhook key resolver")
	}
	encoded, err := a.webhookSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook key: %w", err)
	}
	if encoded == "" {
		return nil, errors.New("webhook key not configured")
	}
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("webhook key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse webhook key: %w", err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("webhook key is not RSA")
	}
	return key, nil
}
