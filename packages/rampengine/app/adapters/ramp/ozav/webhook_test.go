package ozav

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The secret is used with its prefix intact. Ozav is explicit that the
// `ozav_whsec_` prefix is part of the key rather than a display label, and
// stripping it produces a completely different digest — so the test secret
// carries one, and the signing helper below is what a correct sender does.
const testWebhookSecret = "ozav_whsec_2f9c7a1b4e6d8f0a"

func signOzav(t *testing.T, secret string, at time.Time, body []byte) string {
	t.Helper()
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, err := mac.Write([]byte(stamp + "." + string(body)))
	require.NoError(t, err)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookAdapter(secret string) *Adapter {
	return &Adapter{webhookSecrets: func(context.Context) (string, error) { return secret, nil }}
}

func transferEvent() []byte {
	return []byte(`{"id":"ozav_evt_1","object":"event","type":"transfer.completed",
		"created_at":"2026-08-28T20:00:00Z",
		"data":{"id":"ozav_trf_9","object":"transfer","status":"completed"}}`)
}

func signedHeaders(t *testing.T, body []byte) map[string]string {
	t.Helper()
	return map[string]string{
		"X-OZAV-Signature": signOzav(t, testWebhookSecret, time.Now(), body),
		"X-OZAV-Event-ID":  "ozav_evt_1",
	}
}

func TestParseWebhookAcceptsAValidSignature(t *testing.T) {
	body := transferEvent()

	result, err := webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), signedHeaders(t, body), body)

	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindRamp, result.Kind)
	require.Equal(t, "ozav_evt_1", result.EventID)
	require.Equal(t, "transfer.completed", result.EventType)
	require.Equal(t, "ozav_trf_9", result.ProviderOrderID)
}

// The prefix is part of the key. A verifier that strips it — the natural
// mistake, since it looks like a label — rejects every genuine delivery.
func TestParseWebhookRejectsASignatureMadeWithTheStrippedSecret(t *testing.T) {
	body := transferEvent()
	headers := map[string]string{
		"X-OZAV-Signature": signOzav(t, "2f9c7a1b4e6d8f0a", time.Now(), body),
	}

	_, err := webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), headers, body)

	require.ErrorIs(t, err, ErrInvalidSignature)
}

// The timestamp is inside the signed material, so a captured delivery stays
// correctly signed forever. Only the clock refuses a replay.
func TestParseWebhookRejectsAStaleTimestamp(t *testing.T) {
	body := transferEvent()
	headers := map[string]string{
		"X-OZAV-Signature": signOzav(t, testWebhookSecret, time.Now().Add(-30*time.Minute), body),
	}

	_, err := webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), headers, body)

	require.ErrorIs(t, err, ErrInvalidSignature)
}

func TestParseWebhookRejectsAWrongDigest(t *testing.T) {
	body := transferEvent()
	headers := map[string]string{
		"X-OZAV-Signature": "t=" + strconv.FormatInt(time.Now().Unix(), 10) + ",v1=" + hex.EncodeToString(make([]byte, 32)),
	}

	_, err := webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), headers, body)

	require.Equal(t, ErrInvalidSignature, err, "a forgery learns nothing from the refusal, not even in the log")
}

// The signature covers the exact bytes delivered. Re-encoding the parsed
// JSON reorders keys and drops whitespace, so verifying over a
// re-serialization would reject genuine deliveries — this pins that the raw
// body is what gets verified.
func TestParseWebhookVerifiesTheRawBodyNotAReserialization(t *testing.T) {
	body := transferEvent()
	headers := signedHeaders(t, body)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	reserialized, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.NotEqual(t, body, reserialized, "the fixture must actually differ once re-encoded")

	_, err = webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), headers, reserialized)
	require.ErrorIs(t, err, ErrInvalidSignature)

	_, err = webhookAdapter(testWebhookSecret).ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)
}

// A secret this side cannot use is not an open door: the delivery is refused
// like a forgery. But the log has to say why, or a secret that fails to
// decrypt reads as Ozav signing wrong.
func TestParseWebhookSaysWhyTheSecretCannotVerify(t *testing.T) {
	body := transferEvent()
	for _, tc := range []struct {
		name    string
		resolve SecretFactory
		cause   string
	}{
		{"no factory", nil, "no webhook secret resolver"},
		{"resolution fails", func(context.Context) (string, error) { return "", errors.New("decrypt: bad mac") }, "decrypt: bad mac"},
		{"not configured", func(context.Context) (string, error) { return "", nil }, "webhook secret not configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(nil, tc.resolve).ParseWebhook(context.Background(), signedHeaders(t, body), body)

			require.ErrorIs(t, err, ErrInvalidSignature)
			require.ErrorContains(t, err, tc.cause)
		})
	}
}

func TestParseWebhookRejectsAMalformedHeader(t *testing.T) {
	body := transferEvent()
	for _, header := range []string{"", "nonsense", "v1=abc", "t=notanumber,v1=abc", "t=1,v1=nothex"} {
		_, err := webhookAdapter(testWebhookSecret).ParseWebhook(
			context.Background(), map[string]string{"X-OZAV-Signature": header}, body)
		require.ErrorIs(t, err, ErrInvalidSignature, "header %q", header)
	}
}

// DecodeWebhook maps a body whose trust was established earlier — reprocessing
// reads it back out of webhook_events, where the headers no longer exist.
func TestDecodeWebhookMapsEachEventToItsKind(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantKind   ramp.WebhookKind
		wantOrder  string
		wantCust   string
		wantStatus string
	}{
		{
			name: "transfer carries the order",
			body: `{"id":"ozav_evt_1","type":"transfer.completed",
				"data":{"id":"ozav_trf_9","status":"completed"}}`,
			wantKind: ramp.WebhookKindRamp, wantOrder: "ozav_trf_9",
			wantStatus: models.TxStatusCompleted,
		},
		{
			name: "customer carries the customer and its posture",
			body: `{"id":"ozav_evt_2","type":"customer.updated",
				"data":{"id":"ozav_cus_5","status":"approved","verification":{"mode":"full"},
				"requirements":{"due":[],"missing_fields":[]}}}`,
			wantKind: ramp.WebhookKindKyc, wantCust: "ozav_cus_5",
			wantStatus: models.KycStatusApproved,
		},
		{
			name: "a customer in review waiting on a file is incomplete, as GetKyc reads it",
			body: `{"id":"ozav_evt_6","type":"customer.updated",
				"data":{"id":"ozav_cus_5","status":"in_review","verification":{"mode":"reliance"},
				"requirements":{"due":[],"missing_fields":[],"owner":"integrator",
				"documents_due":[{"key":"bank_statement","doc_type":"bank_statement","label":"Extrato","status":"due"}]}}}`,
			wantKind: ramp.WebhookKindKyc, wantCust: "ozav_cus_5",
			wantStatus: models.KycStatusIncomplete,
		},
		{
			name: "verification names the customer, not itself",
			body: `{"id":"ozav_evt_3","type":"verification.updated",
				"data":{"id":"ozav_vrf_7","customer_id":"ozav_cus_5","status":"completed"}}`,
			wantKind: ramp.WebhookKindKyc, wantCust: "ozav_cus_5",
		},
		{
			name:     "webhook.test stays unknown",
			body:     `{"id":"ozav_evt_5","type":"webhook.test","data":{}}`,
			wantKind: ramp.WebhookKindUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), []byte(tc.body))
			require.NoError(t, err)

			require.Equal(t, tc.wantKind, result.Kind)
			require.Equal(t, tc.wantOrder, result.ProviderOrderID)
			require.Equal(t, tc.wantCust, result.ExternalCustomerID)
			require.Equal(t, tc.wantStatus, result.Status)
		})
	}
}

func TestDecodeWebhookDepositCredited(t *testing.T) {
	adapter, _ := newFakeAdapter(t, nil)
	body := []byte(`{"id":"ozav_evt_4","type":"deposit.credited","created_at":"2026-10-06T19:07:05.333Z",
		"data":{"amount":"17","object":"deposit","network":"solana","currency":"BRS",
		"wallet_id":"ozav_wal_1","transaction_hash":"hash17"}}`)
	result, err := adapter.DecodeWebhook(context.Background(), body)
	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindManagedWalletDeposit, result.Kind)
	require.Equal(t, "ozav_wal_1", result.ProviderAccountID)
	require.Empty(t, result.ExternalCustomerID)
	require.NotNil(t, result.ManagedWalletDeposit)
	require.Equal(t, "ozav_wal_1", result.ManagedWalletDeposit.WalletID)
	require.Equal(t, "17", result.ManagedWalletDeposit.Amount)
	require.Equal(t, "hash17", result.ManagedWalletDeposit.TxHash)
}

// A customer event carries the whole customer, so the endorsement it implies
// can be read from it — that is a refresh round trip the service does not
// have to make.
func TestDecodeWebhookCarriesTheEndorsementACustomerEventImplies(t *testing.T) {
	body := []byte(`{"id":"ozav_evt_2","type":"customer.created",
		"data":{"id":"ozav_cus_5","status":"approved","verification":{"mode":"full"},
		"requirements":{"due":[],"missing_fields":[]}}}`)

	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)
	require.NoError(t, err)

	require.Len(t, result.Endorsements, 1)
	require.Equal(t, models.EndorsementPix, result.Endorsements[0].Name)
	require.Equal(t, models.EndorsementStatusApproved, result.Endorsements[0].Status)
}

// The event's own created_at is when it happened, and it is stable across
// retries — which is what makes it usable for ordering, unlike receipt time.
func TestDecodeWebhookReadsTheEventTime(t *testing.T) {
	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), transferEvent())
	require.NoError(t, err)

	require.NotNil(t, result.OccurredAt)
	require.Equal(t, "2026-08-28T20:00:00Z", result.OccurredAt.UTC().Format(time.RFC3339))
}

func TestDecodeWebhookRejectsAnUndecodableBody(t *testing.T) {
	_, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), []byte("not json"))

	require.Error(t, err)
}

// A transfer event carries what actually settled, and the settled figure wins
// over the quote's estimate on the row. Ozav's rate is target per source; the
// row holds BRL per token in both directions, so an on-ramp's 0.1764 USDC per
// BRL is inverted to 5.668934 rather than shown as 0.18 BRL per USDC.
func TestDecodeWebhookCarriesTheSettledTransferFigures(t *testing.T) {
	for _, tc := range []struct {
		name, amounts          string
		wantSource, wantTarget string
		wantRate               string
	}{
		{"off-ramp: token in, BRL out",
			`{"source":{"amount":"100","currency":"USDC"},"target":{"amount":"541.20","currency":"BRL"},"rate":"5.412000"}`,
			"100", "541.20", "5.412000"},
		{"on-ramp: BRL in, token out",
			`{"source":{"amount":"500","currency":"BRL"},"target":{"amount":"88.2","currency":"USDC"},"rate":"0.1764"}`,
			"500", "88.2", "5.668934"},
		{"a delivery: token to token",
			`{"source":{"amount":"88.2","currency":"USDC"},"target":{"amount":"88.2","currency":"USDC"},"rate":"1"}`,
			"88.2", "88.2", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"id":"ozav_evt_7","object":"event","type":"transfer.completed",
				"created_at":"2026-09-02T12:00:00Z",
				"data":{"id":"ozav_trf_9","object":"transfer","status":"completed","amounts":` + tc.amounts + `}}`)

			result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

			require.NoError(t, err)
			require.Equal(t, tc.wantSource, result.SourceAmount)
			require.Equal(t, tc.wantTarget, result.DestinationAmount)
			require.Equal(t, tc.wantRate, result.ExchangeRate)
		})
	}
}

// The status alone was half the news. A customer Ozav moved to in_review with
// a bank statement due decoded as incomplete, but its requirements stayed what
// the last sync had stored -- nothing, for a customer that was under review --
// so the overview read "incomplete, naming nothing" as a review in progress
// and the app told the user to wait for a statement nobody asked them for.
func TestDecodeWebhookCarriesWhatTheCustomerStillOwes(t *testing.T) {
	for _, tc := range []struct {
		name, requirements string
		want               []string
	}{
		{"a file compliance asked for",
			`{"due":[],"missing_fields":[],"owner":"integrator","documents_due":[
				{"key":"bank_statement","doc_type":"bank_statement","label":"Extrato","status":"due"},
				{"key":"proof_of_address","doc_type":"proof_of_address","label":"Comprovante","status":"submitted"}]}`,
			[]string{"bank_statement"}},
		{"items due and fields never supplied",
			`{"due":["identity_document","proof_of_address"],"missing_fields":["occupation"]}`,
			[]string{"identity_document", "proof_of_address", "occupation"}},
		{"nothing, once there is nothing to send",
			`{"due":[],"missing_fields":[]}`,
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"id":"ozav_evt_8","object":"event","type":"customer.updated",
				"data":{"customer":{"id":"ozav_cus_5","status":"in_review","verification":{"mode":"reliance"},
				"requirements":` + tc.requirements + `}}}`)

			result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

			require.NoError(t, err)
			require.True(t, result.ReportsRequirements)
			require.Equal(t, tc.want, result.Requirements)
		})
	}
}

// The webhook is the only way the app hears why a file came back: Ozav sends
// no second event when it re-reviews one.
func TestDecodeWebhookCarriesWhyAFileWasAskedForAgain(t *testing.T) {
	body := []byte(`{"id":"ozav_evt_9","object":"event","type":"customer.updated",
		"data":{"customer":{"id":"ozav_cus_5","status":"in_review","verification":{"mode":"reliance"},
		"requirements":{"due":[],"missing_fields":[],"owner":"integrator","documents_due":[
			{"key":"proof_of_address","doc_type":"proof_of_address","status":"due","reason_code":"document_invalid"}]}}}}`)

	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

	require.NoError(t, err)
	require.Equal(t, []string{models.KycRejectionProofOfAddressInvalid}, result.RejectionCategories)
}

func TestDecodeWebhookAcceptsCustomerWrappedInData(t *testing.T) {
	body := []byte(`{"id":"ozav_evt_live","object":"event","type":"customer.updated",
		"created_at":"2026-09-02T12:00:00Z",
		"data":{"customer":{"id":"ozav_cus_5","status":"approved","verification":{"mode":"standard"},
			"requirements":{"due":[],"missing_fields":[]}}}}`)

	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindKyc, result.Kind)
	require.Equal(t, "ozav_cus_5", result.ExternalCustomerID)
	require.Equal(t, models.KycStatusApproved, result.Status)
}

func TestDecodeWebhookAcceptsTransferWrappedInData(t *testing.T) {
	body := []byte(`{"id":"ozav_evt_live","object":"event","type":"transfer.completed",
		"created_at":"2026-09-02T12:00:00Z",
		"data":{"transfer":{"id":"ozav_trf_9","object":"transfer","status":"completed",
			"amounts":{"source":{"amount":"500","currency":"BRL"},
				"target":{"amount":"88.2","currency":"USDC"},"rate":"0.1764"}}}}`)

	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindRamp, result.Kind)
	require.Equal(t, "ozav_trf_9", result.ProviderOrderID)
	require.Equal(t, models.TxStatusCompleted, result.Status)
}

// liveFailedTransferEvent is ozav_evt_01M6ESRWA4MTJX4ECKQQ9XPTEN as production
// received it on 02/11/2026: status_detail is an object, not the string the
// sandbox captures carried, and decoding it as a string left the event
// "processed" with no order named.
func liveFailedTransferEvent() []byte {
	return []byte(`{"id":"ozav_evt_01M6ESRWA4MTJX4ECKQQ9XPTEN","object":"event","type":"transfer.failed",
		"livemode":true,"created_at":"2026-11-02T18:53:57.956Z",
		"data":{"transfer":{"id":"ozav_tr_01M6ESPYBX2EHDT6G3W18SHVNG","object":"transfer",
			"source":{"type":"wallet","chain":"solana","currency":"USDC","wallet_id":"ozav_wal_01M3NHSTBYAWQ7KJAAZRAKJ6FD"},
			"status":"failed",
			"amounts":{"rate":"5.351","source":{"amount":"20","currency":"USDC"},"target":{"amount":"107.02","currency":"BRL"}},
			"quote_id":"ozav_q_01M6ESJTCYXQR2AP1V6SG86CFT",
			"created_at":"2026-11-02T18:52:54.547109+00:00",
			"source_leg":{"status":"failed"},
			"updated_at":"2026-11-02T18:52:55.646+00:00",
			"customer_id":"ozav_cus_01M1J86F12DHR1ZRK65YCQ666V",
			"destination":{"type":"payout_destination","payout_destination_id":"ozav_pd_01M6ESKBZ82ZGFW9S45C5J1KXB"},
			"status_detail":{"code":"AMOUNT_BELOW_MINIMUM","message":"Minimum amount for this off-ramp is 185 USDC.","retryable":false},
			"destination_leg":{"status":"failed"},
			"exchange_conditions":{"spread_bps":0}}}}`)
}

func TestDecodeWebhookReadsALiveFailedTransfer(t *testing.T) {
	result, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), liveFailedTransferEvent())

	require.NoError(t, err)
	require.Equal(t, ramp.WebhookKindRamp, result.Kind)
	require.Equal(t, "ozav_tr_01M6ESPYBX2EHDT6G3W18SHVNG", result.ProviderOrderID)
	require.Equal(t, "ozav_q_01M6ESJTCYXQR2AP1V6SG86CFT", result.ProviderQuoteID)
	require.Equal(t, models.TxStatusFailed, result.Status)
	require.Equal(t, "AMOUNT_BELOW_MINIMUM: Minimum amount for this off-ramp is 185 USDC.", result.FailureReason)
}

// A transfer event this side cannot read fails, so the event is stamped with
// the error. Read as an empty transfer, it named no order and was stamped
// processed: the failure only surfaced as a user asking where their money is.
func TestDecodeWebhookRefusesATransferItCannotRead(t *testing.T) {
	for name, data := range map[string]string{
		"undecodable":           `{"transfer":{"id":"ozav_tr_1","status":42}}`,
		"wrapped without an id": `{"transfer":{"status":"failed"}}`,
		"bare without an id":    `{"status":"failed"}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"id":"ozav_evt_1","object":"event","type":"transfer.failed","data":` + data + `}`)

			_, err := webhookAdapter(testWebhookSecret).DecodeWebhook(context.Background(), body)

			require.Error(t, err)
		})
	}
}
