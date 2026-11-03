package blindpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrInvalidSignature is every reason a delivery was not accepted: a bad
// digest, a malformed header, a stale timestamp, a missing secret. They are one
// error because the reply is the same for all of them, and telling them apart
// would tell a prober which part of a forgery to fix.
var ErrInvalidSignature = errors.New("blindpay: webhook signature is not valid")

// BlindPay delivers through Svix.
const (
	svixIDHeader        = "svix-id"
	svixTimestampHeader = "svix-timestamp"
	svixSignatureHeader = "svix-signature"
	svixSecretPrefix    = "whsec_"
	svixSignatureScheme = "v1,"
)

// webhookTolerance bounds how far a delivery's timestamp may be from now. The
// timestamp is inside the signed content, which is what makes this the replay
// defence: a captured delivery stays correctly signed forever, and only the
// clock refuses it. Five minutes is Svix's own default.
const webhookTolerance = 5 * time.Minute

// webhookEnvelope is the one field every BlindPay delivery shares. The payload
// is flat — webhook_event beside the resource's own fields — not wrapped in a
// data object.
type webhookEnvelope struct {
	WebhookEvent string `json:"webhook_event"`
}

// ParseWebhook verifies a delivery over the raw body before anything decodes
// it, so a forged payload never reaches a parser.
//
// The event's identity is the Svix message id from the signed headers: the
// payload carries none, and Svix keeps the id across redeliveries.
func (a *Adapter) ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (*ramp.WebhookResult, error) {
	if err := a.verifyWebhook(ctx, headers, body, time.Now()); err != nil {
		return nil, err
	}
	result, err := decodeWebhook(body)
	if err != nil {
		return nil, err
	}
	result.EventID = ramp.HeaderValue(headers, svixIDHeader)
	return result, nil
}

// DecodeWebhook maps a payload that was verified at ingest. Reprocessing reads
// bodies back from webhook_events, where the headers are gone, so it works on
// the body alone.
//
// A customer event carries the customer but not its open request for
// information, which is where the requirements live. So the processor's decode
// — which runs off any transaction — reads the RFI the way the sync does, and
// the event persists the same requirements a poll would. Ingest (ParseWebhook)
// never calls out: the delivery's acknowledgement does not wait on BlindPay.
// A failed RFI read leaves ReportsRequirements unset, so the row keeps what
// the last sync wrote rather than being emptied.
func (a *Adapter) DecodeWebhook(ctx context.Context, body []byte) (*ramp.WebhookResult, error) {
	result, err := decodeWebhook(body)
	if err != nil {
		return nil, err
	}
	// The same flag GetOrder raises. It is raised here, not in decodeWebhook,
	// because ingest decodes every delivery too and would log it twice.
	if result.Kind == ramp.WebhookKindRamp && result.Status == models.TxStatusRefundAttention {
		noun := "payin"
		if isPayoutEvent(result.EventType) {
			noun = "payout"
		}
		facades.Log().Errorf("ramp.attention: blindpay "+noun+" %s %s", result.ProviderOrderID, result.Status)
	}
	if result.Kind == ramp.WebhookKindRamp && isPayoutEvent(result.EventType) {
		var payout payoutResponse
		if json.Unmarshal(result.Raw, &payout) == nil && !payoutStatusKnown(payout.Status) {
			facades.Log().Errorf("ramp.attention: blindpay payout %s has a status it does not know", result.ProviderOrderID)
		}
	}
	if result.Kind == ramp.WebhookKindKyc && result.Status != "" && result.ExternalCustomerID != "" {
		a.attachRequirements(ctx, result)
	}
	return result, nil
}

// attachRequirements reads the customer's open RFI onto a customer event.
func (a *Adapter) attachRequirements(ctx context.Context, result *ramp.WebhookResult) {
	client, err := a.client(ctx)
	if err != nil {
		return
	}
	keys, err := openRFIKeys(ctx, client, result.ExternalCustomerID)
	if err != nil {
		return
	}
	result.Requirements = keys
	result.ReportsRequirements = true
}

// decodeWebhook maps the payload alone, with no call to BlindPay.
//
// Customer events and tos.accept are KYC events — customer.delete one that
// switches the integration off. payin.new, payin.update and payin.complete are
// ramp events, mapped by the same payinStatus every read uses; payin.partnerFee
// is XIP's own fee being booked and moves nothing. payout.new, payout.update
// and payout.complete are ramp events too, mapped by payoutStatus; payout.partnerFee
// is inert like the payin's.
// Everything else is stored and left unknown, which the processor records and
// skips.
func decodeWebhook(body []byte) (*ramp.WebhookResult, error) {
	var envelope webhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("blindpay: decode webhook: %w", err)
	}
	result := &ramp.WebhookResult{
		Kind:      ramp.WebhookKindUnknown,
		EventType: envelope.WebhookEvent,
		Raw:       body,
	}
	switch envelope.WebhookEvent {
	case "customer.new", "customer.update":
		var customer customerResponse
		if err := json.Unmarshal(body, &customer); err != nil {
			return nil, fmt.Errorf("blindpay: decode %s: %w", envelope.WebhookEvent, err)
		}
		state := kycStateFrom(customer, body)
		result.Kind = ramp.WebhookKindKyc
		result.ExternalCustomerID = customer.ID
		result.Status = state.Status
		result.Endorsements = state.Endorsements
		result.RejectionCategories = state.RejectionCategories
		result.RejectionDetails = state.RejectionDetails
	case "customer.delete":
		// The customer is gone at BlindPay: the integration is switched
		// off. The payload is the deletion, not the customer, so no Raw.
		var deleted struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &deleted); err != nil {
			return nil, fmt.Errorf("blindpay: decode customer.delete: %w", err)
		}
		if deleted.ID != "" {
			result.Kind = ramp.WebhookKindKyc
			result.ExternalCustomerID = deleted.ID
			result.IntegrationDisabled = true
			result.Raw = nil
		}
	case "payin.new", "payin.update", "payin.complete":
		var payin payinResponse
		if err := json.Unmarshal(body, &payin); err != nil {
			return nil, fmt.Errorf("blindpay: decode %s: %w", envelope.WebhookEvent, err)
		}
		if payin.ID != "" {
			order, _ := payinOrder(payin, body)
			result.Kind = ramp.WebhookKindRamp
			result.ProviderOrderID = order.ProviderOrderID
			result.Status = order.Status
			result.TxHash = payinTxHash(payin)
			result.SourceAmount = order.Amounts["source_amount"]
			result.DestinationAmount = order.Amounts["destination_amount"]
		}
	case "payout.new", "payout.update", "payout.complete":
		var payout payoutResponse
		if err := json.Unmarshal(body, &payout); err != nil {
			return nil, fmt.Errorf("blindpay: decode %s: %w", envelope.WebhookEvent, err)
		}
		if payout.ID != "" {
			order, _ := payoutOrder(payout, body)
			result.Kind = ramp.WebhookKindRamp
			result.ProviderOrderID = order.ProviderOrderID
			result.Status = order.Status
			result.TxHash = order.TxHash
			result.FailureReason = order.FailureReason
			result.ProviderCustomerID = payout.CustomerID
			result.BankAccountID = payout.BankAccountID
			result.SenderWallet = payout.SenderWalletAddress
			result.SourceAmount = order.Amounts["source_amount"]
			result.DestinationAmount = order.Amounts["destination_amount"]
		}
	case "payout.partnerFee":
		// Inert like payin.partnerFee: the fee is a figure of the payout.
	case "payin.partnerFee":
		// Known, and deliberately inert: the fee is a figure of the payin the
		// other events already carry. Kind stays unknown so the processor
		// records the event and applies nothing, whatever status it names.
	case "tos.accept":
		// Without a customer the acceptance predates it and the app reports it.
		// With one, no Status: the row is refreshed rather than guessed at.
		var accepted struct {
			CustomerID string `json:"customer_id"`
		}
		if err := json.Unmarshal(body, &accepted); err != nil {
			return nil, fmt.Errorf("blindpay: decode tos.accept: %w", err)
		}
		if accepted.CustomerID != "" {
			result.Kind = ramp.WebhookKindKyc
			result.ExternalCustomerID = accepted.CustomerID
			// The payload is the acceptance, not the customer: no Raw, so
			// the row's raw_status keeps the last customer read.
			result.Raw = nil
		}
	}
	return result, nil
}

func isPayoutEvent(event string) bool { return strings.HasPrefix(event, "payout.") }

func (a *Adapter) verifyWebhook(ctx context.Context, headers map[string]string, body []byte, now time.Time) error {
	id := ramp.HeaderValue(headers, svixIDHeader)
	rawTimestamp := ramp.HeaderValue(headers, svixTimestampHeader)
	signatures := ramp.HeaderValue(headers, svixSignatureHeader)
	if id == "" || rawTimestamp == "" || signatures == "" {
		return ErrInvalidSignature
	}
	seconds, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	sent := time.Unix(seconds, 0)
	if now.Sub(sent) > webhookTolerance || sent.Sub(now) > webhookTolerance {
		return ErrInvalidSignature
	}
	key, err := a.signingKey(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, err)
	}
	expected := sign(key, id, rawTimestamp, body)
	for _, candidate := range strings.Fields(signatures) {
		value, ok := strings.CutPrefix(candidate, svixSignatureScheme)
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			continue
		}
		if hmac.Equal(decoded, expected) {
			return nil
		}
	}
	return ErrInvalidSignature
}

// signingKey reads the current secret and decodes it. A secret this side
// cannot use is wrapped with its cause for the log; the caller still answers
// with the one ErrInvalidSignature.
func (a *Adapter) signingKey(ctx context.Context) ([]byte, error) {
	if a.secrets == nil {
		return nil, errors.New("no webhook secret source")
	}
	secret, err := a.secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("read webhook secret: %w", err)
	}
	encoded, ok := strings.CutPrefix(secret, svixSecretPrefix)
	if !ok || encoded == "" {
		return nil, errors.New("webhook secret is not a whsec_ secret")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("webhook secret is not base64")
	}
	return key, nil
}

func sign(key []byte, id, timestamp string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	return mac.Sum(nil)
}

// SignWebhook renders the svix-signature header a delivery of body would
// carry. It exists for tests and the local dev stub, which have to produce
// deliveries this adapter accepts; production never signs.
func SignWebhook(secret, id string, timestamp time.Time, body []byte) string {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, svixSecretPrefix))
	if err != nil {
		return ""
	}
	digest := sign(key, id, strconv.FormatInt(timestamp.Unix(), 10), body)
	return svixSignatureScheme + base64.StdEncoding.EncodeToString(digest)
}
