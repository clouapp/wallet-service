package blindpay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	finalizeKey = "bank-finalize:auth_0001"
	signedTx    = "c2lnbmVkLXRyYW5zYWN0aW9u"
)

// The bytes of the one POST a finalize sends: the quote, the owner and the
// signed transaction, in this order, with nothing else.
const finalizeBody = `{"quote_id":"` + payoutQuote + `","sender_wallet_address":"` + walletAddr + `","signed_transaction":"` + signedTx + `"}`

type recordedCall struct {
	method, path, query, key string
	body                     []byte
}

// payoutServer answers every request through answer(n, call), n counting
// from 1, and records them all.
func payoutServer(t *testing.T, answer func(n int, call recordedCall) (int, string)) (*blindpay.Adapter, func() []recordedCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call := recordedCall{r.Method, strings.TrimPrefix(r.URL.EscapedPath(), instancePrefix), r.URL.RawQuery, r.Header.Get("Idempotency-Key"), body}
		mu.Lock()
		calls = append(calls, call)
		n := len(calls)
		mu.Unlock()
		status, out := answer(n, call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: models.ProviderModeSandbox,
		BaseURL: server.URL + "/v1", Timeout: 300 * time.Millisecond,
	})
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)
	return adapter, func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedCall(nil), calls...)
	}
}

func always(status int, body string) func(int, recordedCall) (int, string) {
	return func(int, recordedCall) (int, string) { return status, body }
}

func finalizeRequest() ramp.FinalizeRequest {
	return ramp.FinalizeRequest{OrderBinding: payoutBinding(), SignedTransaction: signedTx, IdempotencyKey: finalizeKey}
}

func upstream(status int, code, message string) string {
	return fmt.Sprintf(`{"success":false,"code":%q,"message":%q,"trace_id":"tr_1"}`, code, message)
}

// createdBody is the create's answer as the sandbox gives it (S11g): the
// payout, its status, the partner fee, the wallet and the tracking, and
// nothing of the customer, the bank account, the quote, the leg or the amount.
func createdBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"id":                    payoutID,
		"status":                "processing",
		"partner_fee":           0,
		"sender_wallet_address": walletAddr,
		"tracking_transaction":  map[string]any{"step": "processing", "status": nil, "transaction_hash": nil},
		"tracking_payment":      map[string]any{"step": "on_hold"},
		"tracking_liquidity":    map[string]any{"step": "on_hold"},
		"tracking_complete":     map[string]any{"step": "on_hold"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

// createThenRead answers the create with created and the confirming read of
// the payout with read.
func createThenRead(created string, read func() (int, string)) func(int, recordedCall) (int, string) {
	return func(_ int, call recordedCall) (int, string) {
		if call.method == http.MethodPost {
			return 200, created
		}
		if call.method == http.MethodGet && call.path == "payouts/"+payoutID {
			return read()
		}
		return 404, upstream(404, "NOT_FOUND", "x")
	}
}

func TestFinalizeOfframp_SendsTheExactBytesUnderTheKeyAndReturnsThePayout(t *testing.T) {
	created, err := os.ReadFile("testdata/payout_create_sandbox.json")
	require.NoError(t, err)
	read := payoutBody(t, nil)
	adapter, calls := payoutServer(t, createThenRead(string(created), func() (int, string) { return 200, read }))

	order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 2)
	assert.Equal(t, "POST payouts/solana", got[0].method+" "+got[0].path)
	assert.Equal(t, finalizeBody, string(got[0].body))
	assert.Equal(t, finalizeKey, got[0].key)
	assert.Equal(t, "GET payouts/"+payoutID, got[1].method+" "+got[1].path, "the created payout is confirmed by its bound read")
	assert.Empty(t, got[1].key)
	assert.Equal(t, payoutID, order.ProviderOrderID)
	assert.Equal(t, models.TxStatusProcessing, order.Status)
	assert.Equal(t, map[string]string{"source_amount": "100.00", "destination_amount": "98.50"}, order.Amounts)
	assert.JSONEq(t, read, string(order.Raw), "the order is the payout the read held to the binding")
}

// The fields the sandbox's create leaves out are not required of it: the
// read holds them.
func TestFinalizeOfframp_TheCreateNeedsOnlyThePayoutAndTheWallet(t *testing.T) {
	for name, created := range map[string]string{
		"the sandbox shape":     createdBody(t, nil),
		"with the customer":     createdBody(t, map[string]any{"customer_id": customerID}),
		"with the bank account": createdBody(t, map[string]any{"bank_account_id": bankAcctID}),
	} {
		t.Run(name, func(t *testing.T) {
			adapter, calls := payoutServer(t, createThenRead(created, func() (int, string) { return 200, payoutBody(t, nil) }))
			order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
			require.NoError(t, err)
			assert.Equal(t, payoutID, order.ProviderOrderID)
			assert.Len(t, calls(), 2)
		})
	}
}

func TestFinalizeOfframp_AnAnswerThatNamesTheQuoteIsHeldToIt(t *testing.T) {
	created, err := os.ReadFile("testdata/payout_create_with_quote.json")
	require.NoError(t, err)
	adapter, _ := payoutServer(t, createThenRead(string(created), func() (int, string) { return 200, payoutBody(t, nil) }))
	order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
	require.NoError(t, err)
	assert.Equal(t, payoutID, order.ProviderOrderID)
}

// A replay carries the very bytes and the very key of the first attempt: the
// body is built once per call from the request, nothing in it varies.
func TestFinalizeOfframp_AReplayIsByteIdentical(t *testing.T) {
	adapter, calls := payoutServer(t, createThenRead(createdBody(t, nil), func() (int, string) { return 200, payoutBody(t, nil) }))
	for range 2 {
		_, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
		require.NoError(t, err)
	}
	var posts []recordedCall
	for _, call := range calls() {
		if call.method == http.MethodPost {
			posts = append(posts, call)
		}
	}
	require.Len(t, posts, 2)
	assert.Equal(t, posts[0].body, posts[1].body)
	assert.Equal(t, finalizeKey, posts[0].key)
	assert.Equal(t, posts[0].key, posts[1].key)
}

// A create answer that names something other than what the binding describes
// is never accepted, and nothing more is read: the payout may exist, so the
// caller treats it as ambiguous. What the answer leaves out is not a
// difference; what it names must match.
func TestFinalizeOfframp_RefusesAnAnswerThatIsNotTheBoundPayout(t *testing.T) {
	cases := map[string]map[string]any{
		"another customer":   {"customer_id": "re_000000000002"},
		"another wallet":     {"sender_wallet_address": "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU"},
		"another quote":      {"quote_id": "qu_000000000002"},
		"another bank":       {"bank_account_id": "ba_000000000002"},
		"another amount":     {"sender_amount": 10001},
		"no wallet":          {"sender_wallet_address": nil},
		"not a payout id":    {"id": "pi_000000000001"},
		"no id":              {"id": nil},
		"a short payout id":  {"id": "po_1"},
		"a payout id with /": {"id": "po_00000000/01"},
		"another network":    {"network": "solana"},
		"another token":      {"token": "USDC"},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payout create for quote %s does not match its binding", payoutQuote).Once()
			adapter, calls := payoutServer(t, createThenRead(createdBody(t, overrides), func() (int, string) { return 200, payoutBody(t, nil) }))

			order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrBindingMismatch)
			assert.NotContains(t, err.Error(), walletAddr)
			assert.Len(t, calls(), 1)
			logger.AssertExpectations(t)
		})
	}
}

// The read that confirms the created payout is held to the whole binding: a
// payout of another bank account, customer, quote or amount is a mismatch,
// never the order.
func TestFinalizeOfframp_RefusesACreatedPayoutItsReadDoesNotBind(t *testing.T) {
	cases := map[string]map[string]any{
		"another bank account": {"bank_account_id": "ba_000000000002"},
		"another customer":     {"customer_id": "re_000000000002"},
		"another quote":        {"quote_id": "qu_000000000002"},
		"another amount":       {"sender_amount": 10001},
		"another network":      {"network": "solana"},
		"no bank account":      {"bank_account_id": nil},
		"another payout":       {"id": "po_000000000002"},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payout %s does not match its binding", payoutID).Once()
			adapter, calls := payoutServer(t, createThenRead(createdBody(t, nil), func() (int, string) { return 200, payoutBody(t, overrides) }))

			order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrBindingMismatch)
			var refusal *ramp.FinalizeRefusedError
			assert.False(t, errors.As(err, &refusal))
			assert.Len(t, calls(), 2)
			logger.AssertExpectations(t)
		})
	}
}

// A confirming read that does not answer decides nothing: the payout was
// created, so the outcome is ambiguous, never a refusal, never a quote
// condition, never a mismatch. The read is not retried.
func TestFinalizeOfframp_AConfirmingReadThatFailsIsAmbiguous(t *testing.T) {
	cases := map[string]func() (int, string){
		"a 500":                   func() (int, string) { return 500, upstream(500, "INTERNAL_ERROR", "x") },
		"a 503 with a quote code": func() (int, string) { return 503, upstream(503, "QUOTES_EXPIRED", "quote_expired") },
		"a 404":                   func() (int, string) { return 404, upstream(404, "PAYOUTS_NOT_FOUND", "payout_not_found") },
		"a 429":                   func() (int, string) { return 429, upstream(429, "AUTH_RATE_LIMITED", "x") },
		"a refusal code":          func() (int, string) { return 400, upstream(400, "VALIDATION_FAILED", "x") },
		"a used quote code":       func() (int, string) { return 400, upstream(400, "QUOTES_ALREADY_USED", "quote_already_used") },
		"a body that is not json": func() (int, string) { return 200, `<html>` },
		"a timeout": func() (int, string) {
			time.Sleep(900 * time.Millisecond)
			return 200, payoutBody(t, nil)
		},
	}
	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			captureLog(t)
			adapter, calls := payoutServer(t, createThenRead(createdBody(t, nil), read))

			order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
			assert.Nil(t, order)
			require.Error(t, err)
			var refusal *ramp.FinalizeRefusedError
			assert.False(t, errors.As(err, &refusal), "a failed read is not a refusal")
			assert.NotErrorIs(t, err, ramp.ErrEstimateExpired)
			assert.NotErrorIs(t, err, ramp.ErrQuoteAlreadyUsed)
			assert.NotErrorIs(t, err, ramp.ErrBindingMismatch)
			assert.Len(t, calls(), 2, "the confirming read is sent once")
		})
	}
}

// The create and its confirming read share the caller's bound: a read that
// would outlast it is cut short, and the outcome is ambiguous.
func TestFinalizeOfframp_TheConfirmingReadSharesTheCallersDeadline(t *testing.T) {
	adapter, calls := payoutServer(t, func(_ int, call recordedCall) (int, string) {
		time.Sleep(200 * time.Millisecond)
		if call.method == http.MethodPost {
			return 200, createdBody(t, nil)
		}
		return 200, payoutBody(t, nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Millisecond)
	defer cancel()

	order, err := adapter.FinalizeOfframp(ctx, payinRef, finalizeRequest())
	assert.Nil(t, order)
	require.Error(t, err)
	var refusal *ramp.FinalizeRefusedError
	assert.False(t, errors.As(err, &refusal))
	assert.NotErrorIs(t, err, ramp.ErrBindingMismatch)
	assert.Len(t, calls(), 2)
}

// The decision table of the send (D2): what is definitive and what is not.
func TestFinalizeOfframp_ClassifiesTheAnswer(t *testing.T) {
	type outcome int
	const (
		expired outcome = iota
		used
		refused
		ambiguous
	)
	cases := []struct {
		name    string
		status  int
		body    string
		want    outcome
		code    string
		upState int
	}{
		{"quote expired by message", 400, upstream(400, "VALIDATION_FAILED", "quote_expired"), expired, "", 0},
		{"quote expired by code", 400, upstream(400, "QUOTES_EXPIRED", ""), expired, "", 0},
		{"quote already used by message", 400, upstream(400, "VALIDATION_FAILED", "quote_already_used"), used, "", 0},
		{"quote already used by code", 409, upstream(409, "QUOTES_ALREADY_USED", ""), used, "", 0},
		{"validation failed", 400, upstream(400, "VALIDATION_FAILED", "invalid"), refused, "VALIDATION_FAILED", 0},
		{"customer not found", 404, upstream(404, "CUSTOMERS_NOT_FOUND", "nf"), refused, "CUSTOMERS_NOT_FOUND", 0},
		{"wallet not found", 404, upstream(404, "WALLETS_NOT_FOUND", "nf"), refused, "WALLETS_NOT_FOUND", 0},
		{"onboarding incomplete", 400, upstream(400, "CUSTOMERS_ONBOARDING_INCOMPLETE", "x"), refused, "CUSTOMERS_ONBOARDING_INCOMPLETE", 0},
		{"network not supported", 400, upstream(400, "BLOCKCHAIN_NETWORK_NOT_SUPPORTED", "x"), refused, "BLOCKCHAIN_NETWORK_NOT_SUPPORTED", 0},
		{"forbidden", 403, upstream(403, "AUTH_FORBIDDEN", "x"), refused, "AUTH_FORBIDDEN", 0},
		// Not a refusal: a code this side does not know may mean anything.
		{"a new 400 code", 400, upstream(400, "PAYOUTS_SOMETHING_NEW", "x"), ambiguous, "", 400},
		{"a 400 without a code", 400, `{"success":false}`, ambiguous, "", 400},
		{"a 400 that is not json", 400, `<html>bad gateway</html>`, ambiguous, "", 400},
		{"a 422", 422, upstream(422, "CUSTOMERS_INVALID_DATA", "x"), ambiguous, "", 422},
		{"in flight", 409, upstream(409, "VALIDATION_FAILED", "idempotency_key_in_flight"), ambiguous, "", 409},
		{"payload mismatch", 422, upstream(422, "VALIDATION_FAILED", "idempotency_key_payload_mismatch"), ambiguous, "", 422},
		{"rate limited", 429, upstream(429, "AUTH_RATE_LIMITED", "x"), ambiguous, "", 429},
		{"server error", 500, upstream(500, "INTERNAL_ERROR", "x"), ambiguous, "", 500},
		{"bad gateway", 502, upstream(502, "VALIDATION_FAILED", "x"), ambiguous, "", 502},
		{"a definitive code on a 5xx", 503, upstream(503, "VALIDATION_FAILED", "x"), ambiguous, "", 503},
		{"a quote code on a 5xx", 500, upstream(500, "QUOTES_EXPIRED", "quote_expired"), ambiguous, "", 500},
		{"unauthorized", 401, upstream(401, "AUTH_UNAUTHORIZED", "x"), ambiguous, "", 401},
		{"a 200 whose body is not json", 200, `<html>ok</html>`, ambiguous, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			captureLog(t)
			adapter, calls := payoutServer(t, always(c.status, c.body))

			order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
			assert.Nil(t, order)
			require.Error(t, err)
			assert.Len(t, calls(), 1, "a send is never retried by the adapter")

			var refusal *ramp.FinalizeRefusedError
			isRefusal := errors.As(err, &refusal)
			switch c.want {
			case expired:
				require.ErrorIs(t, err, ramp.ErrEstimateExpired)
				assert.False(t, isRefusal)
			case used:
				require.ErrorIs(t, err, ramp.ErrQuoteAlreadyUsed)
				assert.False(t, isRefusal)
			case refused:
				require.True(t, isRefusal, "a recognized definitive 4xx is a refusal")
				assert.Equal(t, c.code, refusal.Code)
			case ambiguous:
				assert.False(t, isRefusal, "an unrecognized answer carries no refusal mark")
				assert.NotErrorIs(t, err, ramp.ErrEstimateExpired)
				assert.NotErrorIs(t, err, ramp.ErrQuoteAlreadyUsed)
				assert.NotErrorIs(t, err, ramp.ErrBindingMismatch)
				if c.upState != 0 {
					var status interface{ HTTPStatus() int }
					require.True(t, errors.As(err, &status), "the raw upstream error is returned")
					assert.Equal(t, c.upState, status.HTTPStatus())
				}
			}
		})
	}
}

func TestFinalizeOfframp_ATimeoutIsAmbiguousAndNotRetried(t *testing.T) {
	adapter, calls := payoutServer(t, func(int, recordedCall) (int, string) {
		time.Sleep(900 * time.Millisecond)
		return 200, payoutBody(t, nil)
	})
	order, err := adapter.FinalizeOfframp(context.Background(), payinRef, finalizeRequest())
	assert.Nil(t, order)
	require.Error(t, err)
	var refusal *ramp.FinalizeRefusedError
	assert.False(t, errors.As(err, &refusal))
	assert.NotErrorIs(t, err, ramp.ErrEstimateExpired)
	assert.Len(t, calls(), 1)
}

func TestFinalizeOfframp_RefusesBeforeAnyRequest(t *testing.T) {
	cases := map[string]struct {
		edit func(*ramp.FinalizeRequest)
		want error
	}{
		"no signed transaction": {func(r *ramp.FinalizeRequest) { r.SignedTransaction = "" }, ramp.ErrRequestNotSent},
		"no idempotency key":    {func(r *ramp.FinalizeRequest) { r.IdempotencyKey = "" }, ramp.ErrInvalidIdempotencyKey},
		"no quote":              {func(r *ramp.FinalizeRequest) { r.QuoteID = "" }, ramp.ErrRequestNotSent},
		"no wallet":             {func(r *ramp.FinalizeRequest) { r.SenderWallet = "" }, ramp.ErrRequestNotSent},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, calls := payoutServer(t, always(200, payoutBody(t, nil)))
			req := finalizeRequest()
			c.edit(&req)
			_, err := adapter.FinalizeOfframp(context.Background(), payinRef, req)
			require.ErrorIs(t, err, c.want)
			assert.Empty(t, calls())
		})
	}
	t.Run("no provider customer", func(t *testing.T) {
		adapter, calls := payoutServer(t, always(200, payoutBody(t, nil)))
		_, err := adapter.FinalizeOfframp(context.Background(), ramp.CustomerRef{}, finalizeRequest())
		require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
		assert.Empty(t, calls())
	})
}

// listPage is one answer of GET payouts.
func listPage(hasMore bool, next string, items ...string) string {
	cursor := "null"
	if next != "" {
		cursor = `"` + next + `"`
	}
	return fmt.Sprintf(`{"data":[%s],"pagination":{"has_more":%t,"next_page":%s,"prev_page":null}}`, strings.Join(items, ","), hasMore, cursor)
}

func otherPayout(t *testing.T, n int) string {
	return payoutBody(t, map[string]any{"id": fmt.Sprintf("po_%012d", n+10), "quote_id": fmt.Sprintf("qu_%012d", n+10)})
}

func TestFindOfframpByQuote_FindsItOnTheSecondPage(t *testing.T) {
	adapter, calls := payoutServer(t, func(n int, _ recordedCall) (int, string) {
		if n == 1 {
			return 200, listPage(true, "po_000000000011", otherPayout(t, 1))
		}
		return 200, listPage(false, "", otherPayout(t, 2), payoutBody(t, nil))
	})
	order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, payoutID, order.ProviderOrderID)

	got := calls()
	require.Len(t, got, 2)
	first, err := url.ParseQuery(got[0].query)
	require.NoError(t, err)
	assert.Equal(t, "GET payouts", got[0].method+" "+got[0].path)
	assert.Equal(t, customerID, first.Get("customer_id"))
	assert.Equal(t, bankAcctID, first.Get("bank_account_id"))
	assert.Equal(t, "100", first.Get("limit"))
	assert.Empty(t, first.Get("starting_after"))
	second, err := url.ParseQuery(got[1].query)
	require.NoError(t, err)
	assert.Equal(t, "po_000000000011", second.Get("starting_after"))
	assert.Equal(t, customerID, second.Get("customer_id"))
}

func TestFindOfframpByQuote_NothingOnAnyPageIsNilNil(t *testing.T) {
	adapter, calls := payoutServer(t, func(n int, _ recordedCall) (int, string) {
		if n == 1 {
			return 200, listPage(true, "po_000000000011", otherPayout(t, 1))
		}
		return 200, listPage(false, "", otherPayout(t, 2))
	})
	order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
	require.NoError(t, err)
	assert.Nil(t, order)
	assert.Len(t, calls(), 2)
}

func TestFindOfframpByQuote_AnEmptyListingIsNilNil(t *testing.T) {
	adapter, _ := payoutServer(t, always(200, listPage(false, "")))
	order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
	require.NoError(t, err)
	assert.Nil(t, order)
}

// An answer that stops before the end is not "there is none": the caller
// would conclude the payout does not exist.
func TestFindOfframpByQuote_AFailedPageIsNeverNilNil(t *testing.T) {
	cases := map[string]func(n int) (int, string){
		"a 500 on the second page": func(n int) (int, string) {
			if n == 1 {
				return 200, listPage(true, "po_000000000011", otherPayout(t, 1))
			}
			return 500, upstream(500, "INTERNAL_ERROR", "x")
		},
		"a 429":                   func(int) (int, string) { return 429, upstream(429, "AUTH_RATE_LIMITED", "x") },
		"a body that is not json": func(int) (int, string) { return 200, `<html>` },
		"an object without pagination": func(int) (int, string) {
			return 200, `{"data":[]}`
		},
		"more pages and no cursor": func(int) (int, string) { return 200, listPage(true, "") },
		"a cursor that does not advance": func(int) (int, string) {
			return 200, listPage(true, "po_000000000011", otherPayout(t, 1))
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			captureLog(t)
			adapter, _ := payoutServer(t, func(n int, _ recordedCall) (int, string) { return answer(n) })
			order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
			assert.Nil(t, order)
			require.Error(t, err)
			assert.ErrorIs(t, err, ramp.ErrProviderFailed)
		})
	}
}

func TestFindOfframpByQuote_StopsAtTheCapOfPages(t *testing.T) {
	adapter, calls := payoutServer(t, func(n int, _ recordedCall) (int, string) {
		return 200, listPage(true, fmt.Sprintf("po_%012d", n+100), otherPayout(t, n))
	})
	order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
	assert.Nil(t, order)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	assert.Len(t, calls(), 20)
}

// The listing is filtered by customer, but a payout of the same quote under
// another customer or bank account is never bound.
func TestFindOfframpByQuote_RefusesAPayoutOfAnotherCustomer(t *testing.T) {
	cases := map[string]map[string]any{
		"customer":     {"customer_id": "re_000000000002"},
		"bank account": {"bank_account_id": "ba_000000000002"},
		"wallet":       {"sender_wallet_address": "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU"},
		"amount":       {"sender_amount": 10001},
		"network":      {"network": "solana"},
		"token":        {"token": "USDC"},
		"no customer":  {"customer_id": nil},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payout listed for quote %s does not match its binding", payoutQuote).Once()
			adapter, _ := payoutServer(t, always(200, listPage(false, "", payoutBody(t, overrides))))
			order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrBindingMismatch)
			assert.NotContains(t, err.Error(), walletAddr)
			logger.AssertExpectations(t)
		})
	}
}

func TestFindOfframpByQuote_ReadsABareArrayToo(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		adapter, _ := payoutServer(t, always(200, "["+payoutBody(t, nil)+"]"))
		order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
		require.NoError(t, err)
		require.NotNil(t, order)
	})
	t.Run("a short array is the end", func(t *testing.T) {
		adapter, calls := payoutServer(t, always(200, "["+otherPayout(t, 1)+"]"))
		order, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
		require.NoError(t, err)
		assert.Nil(t, order)
		assert.Len(t, calls(), 1)
	})
}

func TestFindOfframpByQuote_RefusesBeforeAnyRequest(t *testing.T) {
	adapter, calls := payoutServer(t, always(200, listPage(false, "")))
	binding := payoutBinding()
	binding.QuoteID = ""
	_, err := adapter.FindOfframpByQuote(context.Background(), payinRef, ramp.FindByQuoteRequest{OrderBinding: binding})
	require.Error(t, err)
	_, err = adapter.FindOfframpByQuote(context.Background(), ramp.CustomerRef{}, ramp.FindByQuoteRequest{OrderBinding: payoutBinding()})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Empty(t, calls())
}
