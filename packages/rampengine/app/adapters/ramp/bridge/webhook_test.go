package bridge

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// signer produces a webhook the way Bridge does, so the verifier is exercised
// against a real signature rather than a stubbed comparison.
type signer struct {
	key    *rsa.PrivateKey
	pubPEM string
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return &signer{key: key, pubPEM: string(encoded)}
}

// sign returns the header value for a body at a given time.
func (s *signer) sign(t *testing.T, body []byte, at time.Time) string {
	t.Helper()
	timestamp := strconv.FormatInt(at.UnixMilli(), 10)
	digest := sha256.Sum256([]byte(timestamp + "." + string(body)))
	raw, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return fmt.Sprintf("t=%s,v0=%s", timestamp, base64.StdEncoding.EncodeToString(raw))
}

func (s *signer) adapter() *Adapter {
	return New(nil, func(context.Context) (string, error) { return s.pubPEM, nil })
}

const transferEvent = `{
	"event_id":"evt_1","event_category":"transfer",
	"event_type":"transfer.updated.status_transitioned",
	"event_object_id":"trf_1",
	"event_created_at":"2026-08-06T12:00:00Z",
	"event_object":{"id":"trf_1","state":"payment_processed",
		"receipt":{"destination_tx_hash":"0xabc"}}
}`

const customerEvent = `{
	"event_id":"evt_2","event_category":"customer",
	"event_type":"customer.updated.status_transitioned",
	"event_object_id":"cus_1",
	"event_object":{"id":"cus_1","status":"active",
		"endorsements":[{"name":"pix","status":"approved"}]}
}`

func TestParseWebhookAcceptsASignedTransferEvent(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindRamp, result.Kind)
	require.Equal(t, "evt_1", result.EventID)
	require.Equal(t, "trf_1", result.ProviderOrderID)
	require.Equal(t, models.TxStatusCompleted, result.Status)
	require.Equal(t, "0xabc", result.TxHash)
	require.NotNil(t, result.OccurredAt)
	require.NotEmpty(t, result.Raw)
}

// The settlement fields other tests stub into the WebhookResult directly are
// asserted here off a real payload, so the JSON-to-struct mapping in the
// transfer branch of DecodeWebhook — including the field names Bridge
// actually uses — is exercised end to end, signature verification included.
func TestParseWebhookCarriesTheTransferReceipt(t *testing.T) {
	s := newSigner(t)
	body := []byte(`{
		"event_id":"evt_3","event_category":"transfer",
		"event_type":"transfer.updated.status_transitioned",
		"event_object_id":"trf_1",
		"event_object":{"id":"trf_1","state":"payment_processed","amount":"18.500000",
			"receipt":{"initial_amount":"18.500000","final_amount":"98.50","exchange_rate":"5.405405","exchange_fee":"0.1"}}
	}`)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.Equal(t, "5.405405", result.ExchangeRate)
	require.Equal(t, "18.500000", result.SourceAmount)
	require.Equal(t, "98.50", result.DestinationAmount)
	require.Equal(t, "0.1", result.ExchangeFee)
}

// When the receipt has not settled yet it carries no initial_amount, and the
// source leg must fall back to the transfer's own amount rather than come back
// empty.
func TestParseWebhookFallsBackToAmountWhenReceiptHasNoInitialAmount(t *testing.T) {
	s := newSigner(t)
	body := []byte(`{
		"event_id":"evt_4","event_category":"transfer",
		"event_type":"transfer.updated.status_transitioned",
		"event_object_id":"trf_2",
		"event_object":{"id":"trf_2","state":"payment_processed","amount":"18.500000",
			"receipt":{"final_amount":"98.50","exchange_rate":"5.405405"}}
	}`)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.Equal(t, "18.500000", result.SourceAmount,
		"with no initial_amount in the receipt, the source falls back to the transfer's own amount")
	require.Equal(t, "98.50", result.DestinationAmount)
}

func TestParseWebhookAcceptsASignedCustomerEvent(t *testing.T) {
	s := newSigner(t)
	body := []byte(customerEvent)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindKyc, result.Kind)
	require.Equal(t, "cus_1", result.ExternalCustomerID)
	require.Equal(t, models.KycStatusApproved, result.Status)
	require.Len(t, result.Endorsements, 1)
	require.Equal(t, models.EndorsementStatusApproved, result.Endorsements[0].Status)
}

// The whole point of verifying first: a forged payload must never reach a
// parser, so a tampered body fails before any field of it is read.
func TestParseWebhookRejectsATamperedBody(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	tampered := []byte(`{"event_id":"evt_1","event_category":"transfer","event_object_id":"trf_HIJACKED"}`)
	_, err := s.adapter().ParseWebhook(context.Background(), headers, tampered)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

func TestParseWebhookRejectsAnotherKeysSignature(t *testing.T) {
	signing := newSigner(t)
	verifying := newSigner(t)

	body := []byte(transferEvent)
	headers := map[string]string{WebhookSignatureHeader: signing.sign(t, body, time.Now())}

	_, err := verifying.adapter().ParseWebhook(context.Background(), headers, body)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

// A captured delivery replayed days later is signed correctly and must still
// be refused: the timestamp is inside the signed material precisely so that
// window can be bounded.
func TestParseWebhookRejectsAStaleDelivery(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{
		WebhookSignatureHeader: s.sign(t, body, time.Now().Add(-2*webhookTolerance)),
	}

	_, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

// A clock a little ahead is normal; a delivery from the future beyond the
// tolerance is not.
func TestParseWebhookAllowsSmallClockSkew(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{
		WebhookSignatureHeader: s.sign(t, body, time.Now().Add(30*time.Second)),
	}

	_, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
}

func TestParseWebhookRejectsMalformedHeaders(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)

	for name, header := range map[string]string{
		"absent":           "",
		"no timestamp":     "v0=abc",
		"no signature":     "t=1700000000000",
		"unparseable time": "t=later,v0=abc",
		"not base64":       "t=1700000000000,v0=!!!!",
		"unknown scheme":   "sha256=deadbeef",
	} {
		t.Run(name, func(t *testing.T) {
			headers := map[string]string{}
			if header != "" {
				headers[WebhookSignatureHeader] = header
			}
			_, err := s.adapter().ParseWebhook(context.Background(), headers, body)
			require.ErrorIs(t, err, ErrInvalidSignature)
		})
	}
}

// Header canonicalisation differs between servers and proxies. The lookup must
// not depend on which one delivered the request.
func TestParseWebhookFindsTheHeaderCaseInsensitively(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{"x-webhook-signature": s.sign(t, body, time.Now())}

	_, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
}

// An event we do not model is still authentic. Reporting it as unknown lets
// the caller record and acknowledge it instead of retrying forever.
func TestParseWebhookReportsUnmodelledCategoriesAsUnknown(t *testing.T) {
	s := newSigner(t)
	body := []byte(`{"event_id":"evt_9","event_category":"kyc_link","event_object_id":"kyc_1"}`)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindUnknown, result.Kind)
	require.Equal(t, "evt_9", result.EventID)
}

// A provider with no configured key cannot verify anything, and accepting the
// delivery anyway would be worse than dropping it.
// A key this side cannot use refuses the delivery like a forgery does, but the
// log has to say why: on 25/08 a PEM pasted into the admin lost its newlines
// and every delivery was refused with a line identical to a forgery's.
func TestParseWebhookSaysWhyAConfiguredKeyCannotVerify(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve SecretFactory
		cause   string
	}{
		{"no factory", nil, "no webhook key resolver"},
		{"resolution fails", func(context.Context) (string, error) { return "", errors.New("decrypt: bad mac") }, "decrypt: bad mac"},
		{"not configured", func(context.Context) (string, error) { return "", nil }, "webhook key not configured"},
		{"newlines stripped", func(context.Context) (string, error) {
			return "-----BEGIN PUBLIC KEY----- MIIB -----END PUBLIC KEY-----", nil
		}, "webhook key is not PEM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(nil, tc.resolve).ParseWebhook(context.Background(),
				map[string]string{WebhookSignatureHeader: "t=1,v0=abc"}, []byte(transferEvent))

			require.ErrorIs(t, err, ErrInvalidSignature)
			require.ErrorContains(t, err, tc.cause)
		})
	}
}

// A forgery learns nothing from the refusal, not even in the log.
func TestParseWebhookRefusesAForgeryWithTheBareSentinel(t *testing.T) {
	s := newSigner(t)
	body := []byte(transferEvent)
	headers := map[string]string{WebhookSignatureHeader: newSigner(t).sign(t, body, time.Now())}

	_, err := s.adapter().ParseWebhook(context.Background(), headers, body)

	require.Equal(t, ErrInvalidSignature, err)
}

// --- virtual account activity ------------------------------------------------

// The identities the captured deliveries carry. They are named once because
// every assertion below is about reading them out of the *object*: the
// envelope's `event_object_id` is the activity's own id and is none of these.
const (
	capturedCustomerID       = "2980af75-cd7a-4abf-af0d-9c262f7dec12"
	capturedVirtualAccountID = "337c390b-ba2e-44c7-a916-40aff905d897"
	capturedTxHash           = "0x717fa898a4ec3f8050cb0211f381b1ab9b4a7ebb04f81da26884ff846f78652c"
)

// deliveryFromTestdata returns a captured virtual account delivery, optionally
// rewriting fields of its `event_object`.
//
// The files are real sandbox deliveries. An activity kind Wave 0 could not
// capture — a microdeposit, an activation — is produced by rewriting one field
// of a real envelope, so even those cases are bytes Bridge shaped rather than
// our reading of the documentation. That discipline is the whole point of Wave
// 0: five mapping bugs in this provider came from hand-written payloads.
func deliveryFromTestdata(t *testing.T, name string, activityOverrides map[string]any) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	if len(activityOverrides) == 0 {
		return raw
	}

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))
	activity, ok := envelope["event_object"].(map[string]any)
	require.True(t, ok, "the captured delivery must carry an event_object")
	for key, value := range activityOverrides {
		activity[key] = value
	}
	envelope["event_object"] = activity

	rewritten, err := json.Marshal(envelope)
	require.NoError(t, err)
	return rewritten
}

// This is the test the whole task turns on. Every activity is delivered as
// `virtual_account.activity.created` — funds_received, payment_submitted,
// payment_processed and account_update alike — so the envelope distinguishes
// nothing. Classifying on it would send every deposit to the status default
// and no order would ever complete.
func TestDecodeWebhookClassifiesOnTheActivityTypeNotTheEnvelope(t *testing.T) {
	for _, tc := range []struct {
		file   string
		kind   ramp.WebhookKind
		status string
	}{
		{"va_activity_funds_received.json", ramp.WebhookKindVirtualAccountDeposit, models.TxStatusFundsReceived},
		{"va_activity_payment_submitted.json", ramp.WebhookKindVirtualAccountDeposit, models.TxStatusProcessing},
		{"va_activity_payment_processed.json", ramp.WebhookKindVirtualAccountDeposit, models.TxStatusCompleted},
		{"va_activity_account_update.json", ramp.WebhookKindVirtualAccount, ""},
	} {
		t.Run(tc.file, func(t *testing.T) {
			result, err := New(nil, nil).DecodeWebhook(context.Background(),
				deliveryFromTestdata(t, tc.file, nil))
			require.NoError(t, err)

			// The captured envelope still says the same thing for all four —
			// that is the fact this test exists for. It is asserted on the
			// payload rather than on result.EventType, which is now qualified
			// with the activity so the events table stays readable.
			var envelope struct {
				EventType string `json:"event_type"`
			}
			require.NoError(t, json.Unmarshal(deliveryFromTestdata(t, tc.file, nil), &envelope))
			require.Equal(t, "virtual_account.activity.created", envelope.EventType,
				"the envelope is the one that distinguishes nothing")

			require.Equal(t, tc.kind, result.Kind)

			// Both identities come from inside the object: `event_object_id` is
			// the activity's id, neither the account's nor the deposit's.
			require.Equal(t, capturedCustomerID, result.ExternalCustomerID)
			require.Equal(t, capturedVirtualAccountID, result.ProviderAccountID)

			if tc.status == "" {
				require.Nil(t, result.Deposit,
					"an account_update carries no deposit_id — only transaction-triggered activities do")
				return
			}
			require.NotNil(t, result.Deposit)
			require.Equal(t, tc.status, result.Deposit.Status)
		})
	}
}

// `amount` and `currency` switch legs between activities: "100.0"/"usd" on
// funds_received, "97.5"/"usdc" on the payment events. Reading `amount` as the
// source would put the crypto figure where the reais belong.
func TestDecodeWebhookReadsTheDepositAmountsOffTheReceipt(t *testing.T) {
	for _, tc := range []struct {
		file              string
		sourceAmount      string
		destinationAmount string
		txHash            string
	}{
		// No receipt yet, so this activity's own amount is still the fiat leg.
		{"va_activity_funds_received.json", "100.0", "", ""},
		{"va_activity_payment_submitted.json", "100.0", "97.5", capturedTxHash},
		{"va_activity_payment_processed.json", "100.0", "97.5", capturedTxHash},
	} {
		t.Run(tc.file, func(t *testing.T) {
			result, err := New(nil, nil).DecodeWebhook(context.Background(),
				deliveryFromTestdata(t, tc.file, nil))
			require.NoError(t, err)
			require.NotNil(t, result.Deposit)
			deposit := result.Deposit

			require.Equal(t, capturedDepositID, deposit.DepositID)
			require.Equal(t, capturedVirtualAccountID, deposit.ProviderAccountID)
			require.Equal(t, tc.sourceAmount, deposit.SourceAmount,
				"the source is the receipt's initial_amount, never the activity's own amount")
			require.Equal(t, tc.destinationAmount, deposit.DestinationAmount)
			require.Equal(t, tc.txHash, deposit.TxHash)

			// The fee is known from the first event, before any receipt exists.
			require.Equal(t, "2.5", deposit.FeeAmount)
			require.Equal(t, "0.0", deposit.ExchangeFeeAmount)

			// One delivery cannot name the fiat currency: `currency` has moved
			// to the token by the payment events and the receipt carries no
			// `initial_currency`. The processor reads it off the account row,
			// which holds whichever delivery arrives first.
			require.Empty(t, deposit.SourceCurrency)
			require.Empty(t, deposit.FeeCurrency)

			// The payer is rail-shaped — a wire names originator_name — and is
			// the only handle an operator has on a third-party deposit.
			require.Equal(t, "Sandbox Test Customer", deposit.SenderName)
			require.Equal(t, "Sandbox deposit simulation", deposit.SenderReference)
			require.NotNil(t, deposit.OccurredAt)
			require.NotEmpty(t, deposit.Raw)
		})
	}
}

// Bridge documents microdeposit funds as never onramped. A transaction for one
// would show the user money that does not exist, so the delivery takes the path
// an unmodelled event already takes: recorded, acknowledged, no effect.
func TestDecodeWebhookNeverTurnsAMicrodepositIntoADeposit(t *testing.T) {
	result, err := New(nil, nil).DecodeWebhook(context.Background(),
		deliveryFromTestdata(t, "va_activity_funds_received.json",
			map[string]any{"type": "microdeposit"}))
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindUnknown, result.Kind)
	require.Nil(t, result.Deposit)
	require.Nil(t, result.Account)
	// Authentic and still identified: the event is stored either way.
	require.Equal(t, "wh_t7h4yEL7NPBSVwtwUEk9NKP", result.EventID)
	require.Equal(t, capturedVirtualAccountID, result.ProviderAccountID)
}

// A kind Bridge adds later is a deposit in flight, not an unknown: the money is
// already in the account. The status default keeps the order moving instead of
// freezing it or crediting it.
func TestDecodeWebhookTreatsAnUnfamiliarActivityAsADepositInFlight(t *testing.T) {
	result, err := New(nil, nil).DecodeWebhook(context.Background(),
		deliveryFromTestdata(t, "va_activity_funds_received.json",
			map[string]any{"type": "some_state_bridge_added"}))
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindVirtualAccountDeposit, result.Kind)
	require.NotNil(t, result.Deposit)
	require.Equal(t, models.TxStatusProcessing, result.Deposit.Status)
}

// An account-level activity describes a change, not a transaction.
func TestDecodeWebhookDescribesAnAccountUpdate(t *testing.T) {
	result, err := New(nil, nil).DecodeWebhook(context.Background(),
		deliveryFromTestdata(t, "va_activity_account_update.json", nil))
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindVirtualAccount, result.Kind)
	require.Nil(t, result.Deposit)
	require.NotNil(t, result.Account)

	require.Equal(t, capturedVirtualAccountID, result.Account.ProviderAccountID)
	// `account_updates` is a before/after pair and the current value is the
	// second: the captured delivery is the one the smoke produced by moving the
	// fee from 0.0 to 2.5. Reading the first slot would write the destination
	// the user just replaced.
	require.Equal(t, "0x1111111111111111111111111111111111111111", result.Account.Destination.Address)
	require.Equal(t, "2.5", result.Account.FeePercent)
	require.Equal(t, "ethereum", result.Account.Destination.PaymentRail)
	require.Empty(t, result.Account.Status,
		"an update says nothing about activation, and asserting one would revive a deactivated account")
	require.NotEmpty(t, result.Account.Raw)
}

func TestDecodeWebhookReportsActivationChanges(t *testing.T) {
	for activityType, status := range map[string]string{
		"activation":   models.VirtualAccountStatusActive,
		"deactivation": models.VirtualAccountStatusInactive,
	} {
		t.Run(activityType, func(t *testing.T) {
			result, err := New(nil, nil).DecodeWebhook(context.Background(),
				deliveryFromTestdata(t, "va_activity_account_update.json",
					map[string]any{"type": activityType}))
			require.NoError(t, err)

			require.Equal(t, ramp.WebhookKindVirtualAccount, result.Kind)
			require.NotNil(t, result.Account)
			require.Equal(t, status, result.Account.Status)
		})
	}
}

// The new branch sits behind the signature check like every other one: a
// delivery that cannot be authenticated never reaches the decoder.
func TestParseWebhookAcceptsASignedVirtualAccountDelivery(t *testing.T) {
	s := newSigner(t)
	body := deliveryFromTestdata(t, "va_activity_payment_processed.json", nil)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindVirtualAccountDeposit, result.Kind)
	require.NotNil(t, result.Deposit)
	require.Equal(t, models.TxStatusCompleted, result.Deposit.Status)

	tampered := deliveryFromTestdata(t, "va_activity_payment_processed.json",
		map[string]any{"amount": "999999.0"})
	_, err = s.adapter().ParseWebhook(context.Background(), headers, tampered)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

// With webhooks in place Bridge is no longer a placeholder: it can both place
// an order and close one.
func TestAdapterIsNoLongerAStub(t *testing.T) {
	factory := ramp.NewFactory(New(nil, nil))
	require.True(t, factory.IsImplemented(ramp.ProviderBridge))
}

// The stored event type names the activity, not just its envelope.
//
// Bridge sends "virtual_account.activity.created" for all four activities of a
// deposit. Persisted verbatim, `webhook_events.event_type` and the lag metric
// would show one string for the whole lifecycle and an operator could not tell
// a funds_received from a payment_processed without opening the payload.
func TestVirtualAccountEventTypeNamesTheActivity(t *testing.T) {
	for _, tc := range []struct{ fixture, want string }{
		{"funds_received", "virtual_account.activity.funds_received"},
		{"payment_submitted", "virtual_account.activity.payment_submitted"},
		{"payment_processed", "virtual_account.activity.payment_processed"},
		{"account_update", "virtual_account.activity.account_update"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			body := deliveryFromTestdata(t, "va_activity_"+tc.fixture+".json", nil)

			result, err := New(nil, nil).DecodeWebhook(context.Background(), body)
			require.NoError(t, err)
			require.Equal(t, tc.want, result.EventType)
		})
	}
}

const customerEventWithOutstandingItems = `{
	"event_id":"evt_3","event_category":"customer",
	"event_type":"customer.updated.status_transitioned",
	"event_object_id":"cus_2",
	"event_object":{"id":"cus_2","status":"under_review",
		"requirements_due":["government_id"],
		"rejection_reasons":[{"reason":"document unreadable","developer_reason":"vendor_code_17"}],
		"endorsements":[{"name":"pix","status":"incomplete",
			"requirements":{"missing":{"all_of":["live_selfie"]}}}]}
}`

// A customer event carries what the customer still owes, and the wizard the
// client shows is derived from exactly that. Dropping it here left the row
// describing whatever the last poll had seen.
func TestDecodeWebhookCarriesRequirementsAndRejectionReasons(t *testing.T) {
	s := newSigner(t)
	body := []byte(customerEventWithOutstandingItems)
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindKyc, result.Kind)
	require.Equal(t, []string{"government_id", "live_selfie"}, result.Requirements)
	require.Equal(t, []string{"document unreadable"}, result.RejectionReasons,
		"developer_reason names vendor internals and is not for the person reading it")
}

// The client renders the terms page in a WebView with a JavaScript bridge
// attached, and closes it on any host outside the session's allowlist. Against
// the real Bridge that list is fixed, byte for byte; against anything else
// (cmd/devstub) the page lives on neither bridge.xyz host, so the list names
// the configured base and the page the stub handed out.
func TestCreateTosSessionCarriesTheHostsItsPageMayLoadFrom(t *testing.T) {
	bridgeHosts := []string{"dashboard.bridge.xyz", "compliance.bridge.xyz", "compliance.sandbox.bridge.xyz", "bridge.xyz"}
	for _, tc := range []struct {
		name, baseURL, pageURL string
		want                   []string
	}{
		{"sandbox", SandboxBaseURL, "https://dashboard.bridge.xyz/accept-terms-of-service?session_token=t", bridgeHosts},
		{"production", ProductionBaseURL, "https://dashboard.bridge.xyz/accept-terms-of-service?session_token=t", bridgeHosts},
		{"a dev stub", "https://127.0.0.1:4310/bridge",
			"http://10.0.2.2:4311/bridge/terms?redirect_uri=x", []string{"127.0.0.1", "10.0.2.2"}},
		{"a dev stub on one host", "https://172.23.204.219:4310/bridge",
			"http://172.23.204.219:4311/bridge/terms?redirect_uri=x", []string{"172.23.204.219"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := adapterDialing(t, tc.baseURL, `{"url":"`+tc.pageURL+`"}`)

			session, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{CustomerID: 7},
				ramp.TosOpts{IdempotencyKey: "idem-key-0123456"})

			require.NoError(t, err)
			require.Equal(t, tc.pageURL, session.URL)
			require.Equal(t, tc.want, session.AllowedHosts)
		})
	}
}

// adapterDialing builds an adapter configured for baseURL whose every request
// is answered with body by a local server, whatever host baseURL names.
func adapterDialing(t *testing.T, baseURL, body string) *Adapter {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	local, err := url.Parse(server.URL)
	require.NoError(t, err)

	client := NewClient(Config{BaseURL: baseURL, APIKey: "sk", HTTPClient: &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme, r.URL.Host = local.Scheme, local.Host
			return http.DefaultTransport.RoundTrip(r)
		}),
	}})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A delivery carries the rail the money came on: the captured wire, and an
// ACH deposit shaped like Bridge's documented example.
func TestDecodeWebhookReportsTheDepositRail(t *testing.T) {
	for file, want := range map[string]string{
		"va_activity_funds_received.json":     "wire",
		"va_activity_ach_funds_received.json": "ach",
	} {
		t.Run(file, func(t *testing.T) {
			result, err := New(nil, nil).DecodeWebhook(context.Background(), deliveryFromTestdata(t, file, nil))
			require.NoError(t, err)
			require.NotNil(t, result.Deposit)
			require.Equal(t, want, result.Deposit.PaymentRail)
		})
	}
}
