package blindpay_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

var testSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

const payoutUpdate = `{"webhook_event":"payout.update","id":"po_000000000001","status":"processing"}`

func secretFactory(secret string, err error) blindpay.SecretFactory {
	return func(context.Context) (string, error) { return secret, err }
}

func signedHeaders(secret, id string, at time.Time, body string) map[string]string {
	return map[string]string{
		"Svix-Id":        id,
		"Svix-Timestamp": strconv.FormatInt(at.Unix(), 10),
		"Svix-Signature": blindpay.SignWebhook(secret, id, at, []byte(body)),
	}
}

func TestParseWebhook_AcceptsAValidDelivery(t *testing.T) {
	adapter := blindpay.New(nil, secretFactory(testSecret, nil), nil)
	result, err := adapter.ParseWebhook(context.Background(),
		signedHeaders(testSecret, "msg_1", time.Now(), payoutUpdate), []byte(payoutUpdate))
	require.NoError(t, err)
	assert.Equal(t, "msg_1", result.EventID, "the svix message id is the event's identity")
	assert.Equal(t, "payout.update", result.EventType)
	assert.Equal(t, ramp.WebhookKindRamp, result.Kind, "a payout event is a ramp event")
	assert.Equal(t, models.TxStatusProcessing, result.Status)
	assert.Equal(t, "po_000000000001", result.ProviderOrderID)
}

// Svix sends every signature the endpoint's secrets produced, space separated,
// during a rotation. Any one of them matching is enough.
func TestParseWebhook_AcceptsOneMatchAmongSeveralSignatures(t *testing.T) {
	adapter := blindpay.New(nil, secretFactory(testSecret, nil), nil)
	headers := signedHeaders(testSecret, "msg_2", time.Now(), payoutUpdate)
	headers["Svix-Signature"] = "v1,bm90LXRoaXMtb25l " + headers["Svix-Signature"]
	_, err := adapter.ParseWebhook(context.Background(), headers, []byte(payoutUpdate))
	assert.NoError(t, err)
}

func TestParseWebhook_RefusesEveryForgery(t *testing.T) {
	now := time.Now()
	otherSecret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("another-secret-another-secret-00"))
	cases := map[string]struct {
		secret  blindpay.SecretFactory
		headers map[string]string
		body    string
	}{
		"tampered body": {secretFactory(testSecret, nil),
			signedHeaders(testSecret, "msg", now, payoutUpdate), `{"webhook_event":"payout.update","id":"po_x"}`},
		"other secret": {secretFactory(testSecret, nil),
			signedHeaders(otherSecret, "msg", now, payoutUpdate), payoutUpdate},
		"stale timestamp": {secretFactory(testSecret, nil),
			signedHeaders(testSecret, "msg", now.Add(-6*time.Minute), payoutUpdate), payoutUpdate},
		"future timestamp": {secretFactory(testSecret, nil),
			signedHeaders(testSecret, "msg", now.Add(6*time.Minute), payoutUpdate), payoutUpdate},
		"no headers":         {secretFactory(testSecret, nil), map[string]string{}, payoutUpdate},
		"no secret set":      {secretFactory("", nil), signedHeaders(testSecret, "msg", now, payoutUpdate), payoutUpdate},
		"secret unreadable":  {secretFactory("", errors.New("settings down")), signedHeaders(testSecret, "msg", now, payoutUpdate), payoutUpdate},
		"secret not base64":  {secretFactory("whsec_%%%", nil), signedHeaders(testSecret, "msg", now, payoutUpdate), payoutUpdate},
		"nil secret factory": {nil, signedHeaders(testSecret, "msg", now, payoutUpdate), payoutUpdate},
	}
	for name, c := range cases {
		adapter := blindpay.New(nil, c.secret, nil)
		_, err := adapter.ParseWebhook(context.Background(), c.headers, []byte(c.body))
		assert.ErrorIs(t, err, blindpay.ErrInvalidSignature, name)
	}
}

// Reprocessing reads the body back from webhook_events, where the headers are
// gone; DecodeWebhook must work on the body alone.
func TestDecodeWebhook_ReadsTheFlatPayload(t *testing.T) {
	adapter := blindpay.New(nil, nil, nil)
	result, err := adapter.DecodeWebhook(context.Background(), []byte(`{"webhook_event":"tos.accept","tos_id":"to_1","idempotency_key":"k"}`))
	require.NoError(t, err)
	assert.Equal(t, "tos.accept", result.EventType)
	assert.Equal(t, ramp.WebhookKindUnknown, result.Kind)
	assert.Empty(t, result.EventID)
}

func TestDecodeWebhook_RefusesABodyThatIsNotJSON(t *testing.T) {
	_, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte("not json"))
	assert.Error(t, err)
}

func TestDecodeWebhook_CustomerUpdateIsAKycEvent(t *testing.T) {
	body := `{"webhook_event":"customer.update","id":"cu_9","customer_id":"cu_9","country":"BR","kyc_type":"standard","kyc_status":"approved","tos_id":"to_1","limit":{"per_transaction":1,"daily":1,"monthly":1}}`
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte(body))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindKyc, result.Kind)
	assert.Equal(t, "cu_9", result.ExternalCustomerID)
	assert.Equal(t, models.KycStatusApproved, result.Status)
	assert.Equal(t, "customer.update", result.EventType)
	assert.JSONEq(t, body, string(result.Raw))
	require.Len(t, result.Endorsements, 2)
	assert.Equal(t, models.EndorsementStatusApproved, result.Endorsements[0].Status)
	assert.Equal(t, models.EndorsementStatusApproved, result.Endorsements[1].Status)
}

func TestDecodeWebhook_RejectedCustomerCarriesItsReasons(t *testing.T) {
	body := `{"webhook_event":"customer.new","id":"cu_9","kyc_status":"rejected","kyc_warnings":[{"code":"name_mismatch"}]}`
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte(body))
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusRejected, result.Status)
	assert.Equal(t, []string{models.KycRejectionDataMismatch}, result.RejectionCategories)
	assert.Equal(t, []string{"name_mismatch"}, result.RejectionDetails)
}

func TestDecodeWebhook_TosAcceptWithoutACustomerIsLeftToTheApp(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(),
		[]byte(`{"webhook_event":"tos.accept","tos_id":"to_1","idempotency_key":"k"}`))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindUnknown, result.Kind)
}

func TestDecodeWebhook_TosAcceptForACustomerRefreshesIt(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(),
		[]byte(`{"webhook_event":"tos.accept","tos_id":"to_2","idempotency_key":"k","customer_id":"cu_9"}`))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindKyc, result.Kind)
	assert.Equal(t, "cu_9", result.ExternalCustomerID)
	assert.Empty(t, result.Status)
}

// The terms payload is not the customer: the KYC event built from it carries
// no Raw, so the processor leaves the row's raw_status (the last customer
// read) as it was rather than writing the acceptance over it.
func TestDecodeWebhook_TosAcceptForACustomerKeepsTheRawStatus(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(),
		[]byte(`{"webhook_event":"tos.accept","tos_id":"to_2","idempotency_key":"k","customer_id":"cu_9"}`))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindKyc, result.Kind)
	assert.Nil(t, result.Raw)
}

// Without a customer the event is not a KYC event and keeps its payload.
func TestDecodeWebhook_TosAcceptWithoutACustomerKeepsItsPayload(t *testing.T) {
	body := `{"webhook_event":"tos.accept","tos_id":"to_1","idempotency_key":"k"}`
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte(body))
	require.NoError(t, err)
	assert.JSONEq(t, body, string(result.Raw))
}

// A deleted customer switches the integration off: a KYC event naming the
// customer, no status to write over the row, and no Raw — the payload is the
// deletion, not the customer.
func TestDecodeWebhook_CustomerDeleteSwitchesTheIntegrationOff(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(),
		[]byte(`{"webhook_event":"customer.delete","id":"cu_9"}`))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindKyc, result.Kind)
	assert.Equal(t, "customer.delete", result.EventType)
	assert.Equal(t, "cu_9", result.ExternalCustomerID)
	assert.True(t, result.IntegrationDisabled)
	assert.Empty(t, result.Status)
	assert.Nil(t, result.Raw)
}

type customerWebhookProbe struct {
	WebhookEvent string         `json:"webhook_event"`
	ID           string         `json:"id"`
	KycType      string         `json:"kyc_type"`
	KycStatus    string         `json:"kyc_status"`
	TosID        string         `json:"tos_id"`
	Limit        map[string]any `json:"limit"`
}

type tosAcceptProbe struct {
	WebhookEvent   string `json:"webhook_event"`
	TosID          string `json:"tos_id"`
	IdempotencyKey string `json:"idempotency_key"`
	CustomerID     string `json:"customer_id"`
}

func TestWebhookPayloadsMatchTheContract(t *testing.T) {
	requireWebhookFields(t, "customer.update", customerWebhookProbe{})
	requireWebhookFields(t, "tos.accept", tosAcceptProbe{})
}

const customerRFIEvent = `{"webhook_event":"customer.update","id":"cu_1","customer_id":"cu_1","kyc_type":"standard","kyc_status":"compliance_request","tos_id":"to_1"}`

// A customer event does not carry the RFI; the processor's decode reads it so
// the event persists the requirements a poll would.
func TestDecodeWebhook_CustomerEventReadsTheOpenRFI(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix + "/rfi": reply(`{"status":"pending","request":[{"fields":[{"key":"source_of_funds_doc"}]}]}`),
	})
	result, err := adapter.DecodeWebhook(context.Background(), []byte(customerRFIEvent))
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusRequiresInfo, result.Status)
	assert.True(t, result.ReportsRequirements)
	assert.Equal(t, []string{"source_of_funds_doc"}, result.Requirements)
}

func TestDecodeWebhook_CustomerEventWithNoOpenRFIOwesNothing(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix + "/rfi": reply(`null`),
	})
	result, err := adapter.DecodeWebhook(context.Background(), []byte(customerRFIEvent))
	require.NoError(t, err)
	assert.True(t, result.ReportsRequirements, "an empty answer means owes nothing")
	assert.Empty(t, result.Requirements)
}

// A failed RFI read says nothing about the requirements, so the row keeps
// what the last sync wrote; the status still applies.
func TestDecodeWebhook_RFIFailureLeavesRequirementsUnreported(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix + "/rfi": failing,
	})
	result, err := adapter.DecodeWebhook(context.Background(), []byte(customerRFIEvent))
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusRequiresInfo, result.Status)
	assert.False(t, result.ReportsRequirements)
}

// Ingest acknowledges the delivery without calling BlindPay.
func TestParseWebhook_CustomerEventCallsNothing(t *testing.T) {
	client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: "http://127.0.0.1:1/v1"})
	var calls atomic.Int32
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) {
		calls.Add(1)
		return client, nil
	}, secretFactory(testSecret, nil), nil)
	result, err := adapter.ParseWebhook(context.Background(),
		signedHeaders(testSecret, "msg_rfi", time.Now(), customerRFIEvent), []byte(customerRFIEvent))
	require.NoError(t, err)
	assert.False(t, result.ReportsRequirements)
	assert.Zero(t, calls.Load())
}

// payinEvent is a payin webhook: the payin's own fields beside webhook_event.
func payinEvent(t *testing.T, event string, overrides map[string]any) []byte {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(payinBody(t, overrides)), &body))
	body["webhook_event"] = event
	out, err := json.Marshal(body)
	require.NoError(t, err)
	return out
}

func TestDecodeWebhook_PayinEventsAreRampEvents(t *testing.T) {
	for _, event := range []string{"payin.new", "payin.update", "payin.complete"} {
		t.Run(event, func(t *testing.T) {
			body := payinEvent(t, event, nil)
			result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), body)
			require.NoError(t, err)
			assert.Equal(t, ramp.WebhookKindRamp, result.Kind)
			assert.Equal(t, event, result.EventType)
			assert.Equal(t, payinID, result.ProviderOrderID)
			assert.Equal(t, models.TxStatusAwaitingFunds, result.Status)
			assert.Equal(t, "100.55", result.SourceAmount)
			assert.Equal(t, "19.60", result.DestinationAmount)
			assert.Empty(t, result.TxHash)
			assert.JSONEq(t, string(body), string(result.Raw))
		})
	}
}

func TestDecodeWebhook_PayinCompleteCarriesTheHash(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payinEvent(t, "payin.complete", map[string]any{
		"status":            "completed",
		"tracking_payment":  map[string]any{"step": "completed"},
		"tracking_complete": map[string]any{"step": "completed", "transaction_hash": "5sig"},
	}))
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusCompleted, result.Status)
	assert.Equal(t, "5sig", result.TxHash)
}

func TestDecodeWebhook_PayinStatusFollowsTheCanonicalTable(t *testing.T) {
	paid := map[string]any{"step": "completed"}
	cases := map[string]struct {
		overrides map[string]any
		want      string
	}{
		"processing and unpaid":  {map[string]any{"status": "processing"}, models.TxStatusAwaitingFunds},
		"processing and paid":    {map[string]any{"status": "processing", "tracking_payment": paid}, models.TxStatusProcessing},
		"failed and unpaid":      {map[string]any{"status": "failed"}, models.TxStatusExpired},
		"refunded":               {map[string]any{"status": "refunded"}, models.TxStatusRefunded},
		"refund review":          {map[string]any{"status": "on_hold", "tracking_transaction": map[string]any{"step": "pending_refund_review"}}, models.TxStatusReturning},
		"failed after a payment": {map[string]any{"status": "failed", "tracking_payment": paid}, models.TxStatusRefundAttention},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.want == models.TxStatusRefundAttention {
				logger := captureLog(t)
				logger.EXPECT().Errorf("ramp.attention: blindpay payin %s %s", payinID, models.TxStatusRefundAttention).Once()
				defer logger.AssertExpectations(t)
			}
			result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payinEvent(t, "payin.update", tc.overrides))
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Status)
		})
	}
}

// Ingest decodes the delivery as well; the flag is raised once, by the
// processor's decode, not on both.
func TestParseWebhook_RefundAttentionIsNotLoggedAtIngest(t *testing.T) {
	logger := captureLog(t) // any Errorf is unexpected and fails the mock
	body := payinEvent(t, "payin.update", map[string]any{
		"status": "failed", "tracking_payment": map[string]any{"step": "completed"},
	})
	now := time.Now()
	adapter := blindpay.New(nil, secretFactory(testSecret, nil), nil)
	result, err := adapter.ParseWebhook(context.Background(), signedHeaders(testSecret, "msg_1", now, string(body)), body)
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusRefundAttention, result.Status)
	logger.AssertExpectations(t)
}

// payin.partnerFee is a known event that changes nothing: whatever status it
// carries, the processor sees an unknown kind with no order.
func TestDecodeWebhook_PayinPartnerFeeIsInert(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payinEvent(t, "payin.partnerFee", map[string]any{"status": "completed"}))
	require.NoError(t, err)
	assert.Equal(t, "payin.partnerFee", result.EventType)
	assert.Equal(t, ramp.WebhookKindUnknown, result.Kind)
	assert.Empty(t, result.ProviderOrderID)
	assert.Empty(t, result.Status)
}

func TestDecodeWebhook_PayinWithoutAnIDIsLeftUnknown(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte(`{"webhook_event":"payin.update","status":"completed"}`))
	require.NoError(t, err)
	assert.Equal(t, ramp.WebhookKindUnknown, result.Kind)
}

func TestWebhookPayinPayloadsMatchTheContract(t *testing.T) {
	for _, event := range []string{"payin.new", "payin.update", "payin.complete", "payin.partnerFee"} {
		requireWebhookFields(t, event, payinWebhookProbe{})
	}
}

type payinWebhookProbe struct {
	WebhookEvent        string `json:"webhook_event"`
	ID                  string `json:"id"`
	Status              string `json:"status"`
	CustomerID          string `json:"customer_id"`
	SenderAmount        int64  `json:"sender_amount"`
	ReceiverAmount      int64  `json:"receiver_amount"`
	PixCode             string `json:"pix_code"`
	TrackingTransaction any    `json:"tracking_transaction"`
	TrackingPayment     any    `json:"tracking_payment"`
	TrackingComplete    any    `json:"tracking_complete"`
}

// payoutEvent is a payout webhook: the payout's own fields beside webhook_event.
func payoutEvent(t *testing.T, event string, overrides map[string]any) []byte {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(payoutBody(t, overrides)), &body))
	body["webhook_event"] = event
	out, err := json.Marshal(body)
	require.NoError(t, err)
	return out
}

// One row of the status table per payout event kind: the decode is the same
// payoutStatus every read uses.
func TestDecodeWebhook_PayoutStatusFollowsTheCanonicalTable(t *testing.T) {
	collected := map[string]any{"step": "completed", "status": "found", "transaction_hash": "5collect"}
	cases := map[string]struct {
		overrides map[string]any
		want      string
	}{
		"processing":            {map[string]any{"status": "processing"}, models.TxStatusProcessing},
		"on_hold":               {map[string]any{"status": "on_hold"}, models.TxStatusProcessing},
		"completed":             {map[string]any{"status": "completed"}, models.TxStatusCompleted},
		"refunded":              {map[string]any{"status": "refunded"}, models.TxStatusRefunded},
		"tokens refunded":       {map[string]any{"status": "processing", "tracking_complete": map[string]any{"step": "completed", "status": "tokens_refunded"}}, models.TxStatusRefunded},
		"failed, not collected": {map[string]any{"status": "failed"}, models.TxStatusFailed},
		"failed, collected":     {map[string]any{"status": "failed", "tracking_transaction": collected}, models.TxStatusRefundAttention},
		"refund review":         {map[string]any{"status": "on_hold", "tracking_payment": map[string]any{"step": "pending_refund_review"}}, models.TxStatusReturning},
		"payment returned":      {map[string]any{"status": "processing", "tracking_payment": map[string]any{"step": "completed", "provider_status": "returned"}}, models.TxStatusRefundAttention},
		"payment failed":        {map[string]any{"status": "processing", "tracking_payment": map[string]any{"step": "completed", "provider_status": "failed"}}, models.TxStatusRefundAttention},
		"payment canceled":      {map[string]any{"status": "processing", "tracking_payment": map[string]any{"step": "completed", "provider_status": "canceled"}}, models.TxStatusRefundAttention},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.want == models.TxStatusRefundAttention {
				logger := captureLog(t)
				logger.EXPECT().Errorf("ramp.attention: blindpay payout %s %s", payoutID, models.TxStatusRefundAttention).Once()
				defer logger.AssertExpectations(t)
			}
			result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.update", tc.overrides))
			require.NoError(t, err)
			assert.Equal(t, ramp.WebhookKindRamp, result.Kind)
			assert.Equal(t, tc.want, result.Status)
		})
	}
}

func TestDecodeWebhook_PayoutCarriesTheFailureReason(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.update", map[string]any{
		"status":               "failed",
		"tracking_transaction": map[string]any{"step": "completed", "status": "failed", "provider_error_reason": "erc20_allowance_insufficient"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "erc20_allowance_insufficient", result.FailureReason)
}

// Ingest decodes the delivery too; the flag is raised once, by the processor's
// decode.
func TestParseWebhook_PayoutRefundAttentionIsNotLoggedAtIngest(t *testing.T) {
	logger := captureLog(t)
	body := payoutEvent(t, "payout.update", map[string]any{
		"status":               "failed",
		"tracking_transaction": map[string]any{"step": "completed", "status": "found", "transaction_hash": "5collect"},
	})
	adapter := blindpay.New(nil, secretFactory(testSecret, nil), nil)
	result, err := adapter.ParseWebhook(context.Background(), signedHeaders(testSecret, "msg_1", time.Now(), string(body)), body)
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusRefundAttention, result.Status)
	logger.AssertExpectations(t)
}

func TestDecodeWebhook_PayoutUnknownStatusIsProcessingAndLogged(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payout %s has a status it does not know", payoutID).Once()
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.update", map[string]any{"status": "mystery"}))
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusProcessing, result.Status)
	logger.AssertExpectations(t)
}

func TestDecodeWebhook_PayoutPartnerFeeIsInert(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.partnerFee", map[string]any{"status": "completed"}))
	require.NoError(t, err)
	assert.Equal(t, "payout.partnerFee", result.EventType)
	assert.Equal(t, ramp.WebhookKindUnknown, result.Kind)
	assert.Empty(t, result.ProviderOrderID)
	assert.Empty(t, result.Status)
}

func TestDecodeWebhook_PayoutWithoutAnIDIsLeftUnknown(t *testing.T) {
	for _, event := range []string{"payout.new", "payout.update", "payout.complete"} {
		result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), []byte(`{"webhook_event":"`+event+`","status":"completed"}`))
		require.NoError(t, err)
		assert.Equal(t, ramp.WebhookKindUnknown, result.Kind, event)
		assert.Empty(t, result.ProviderOrderID)
	}
}

func TestWebhookPayoutPayloadsMatchTheContract(t *testing.T) {
	for _, event := range []string{"payout.new", "payout.update", "payout.complete", "payout.partnerFee"} {
		requireWebhookFields(t, event, payoutWebhookProbe{})
	}
}

// payoutWebhookProbe is what the decode reads of a payout delivery. The
// webhook schema declares no quote_id, network, token or amount, so a webhook
// is never a binding check: the status it carries is applied by the
// processor against the recorded binding's ids.
type payoutWebhookProbe struct {
	WebhookEvent        string `json:"webhook_event"`
	ID                  string `json:"id"`
	Status              string `json:"status"`
	CustomerID          string `json:"customer_id"`
	BankAccountID       string `json:"bank_account_id"`
	SenderWalletAddress string `json:"sender_wallet_address"`
	TrackingTransaction any    `json:"tracking_transaction"`
	TrackingPayment     any    `json:"tracking_payment"`
	TrackingComplete    any    `json:"tracking_complete"`
}

// The payout payload carries no quote, network, token or amount the binding
// could be held to, but it does name who and where; the processor gates the
// status on those.
func TestDecodeWebhook_PayoutExposesTheIdentityFields(t *testing.T) {
	result, err := blindpay.New(nil, nil, nil).DecodeWebhook(context.Background(), payoutEvent(t, "payout.update", nil))
	require.NoError(t, err)
	assert.Equal(t, customerID, result.ProviderCustomerID)
	assert.Equal(t, bankAcctID, result.BankAccountID)
	assert.Equal(t, walletAddr, result.SenderWallet)
}
