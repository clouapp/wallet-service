package blindpay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

type feeCall struct {
	method, path, idempotency string
	body                      []byte
}

// feeServer answers GET /partner-fees with list and POST with created, and
// records every call.
func feeServer(t *testing.T, list, created string) (*blindpay.Client, func() []feeCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []feeCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, feeCall{r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, list)
			return
		}
		_, _ = io.WriteString(w, created)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1",
	})
	return client, func() []feeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]feeCall(nil), calls...)
	}
}

// feeProfile is a partner fee profile as BlindPay lists it: XIP's name for
// payin and payout bps, charging exactly those with no flat fee.
func feeProfile(id string, payinBps, payoutBps int) string {
	return fmt.Sprintf(`{"id":%q,"name":"xip-fee-in%d-out%d","payin_percentage_fee":%d,"payin_flat_fee":0,`+
		`"payout_percentage_fee":%d,"payout_flat_fee":0}`, id, payinBps, payoutBps, payinBps, payoutBps)
}

func TestFeeBps(t *testing.T) {
	cases := map[string]int{"": 0, "0": 0, "0.00": 0, "1": 100, "1.5": 150, "1.55": 155, "1.555": 155, "99": 9900}
	for in, want := range cases {
		got, err := blindpay.FeeBps(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	got, err := blindpay.FeeBps("99.001")
	require.NoError(t, err)
	assert.Equal(t, 9900, got)
	got, err = blindpay.FeeBps("1.559999999999999999999999999999")
	require.NoError(t, err)
	assert.Equal(t, 155, got)
	for _, bad := range []string{"abc", "-1", "99.01", "1e2", " 1.5", "1,5"} {
		_, err := blindpay.FeeBps(bad)
		assert.ErrorIs(t, err, ramp.ErrRequestNotSent, bad)
	}
}

func TestPartnerFeeIsFoundByNameBeforeCreating(t *testing.T) {
	client, calls := feeServer(t, `[`+feeProfile("pf_other", 100, 0)+`,`+feeProfile("pf_1", 155, 0)+`]`, `{}`)
	id, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 155, 0)
	require.NoError(t, err)
	assert.Equal(t, "pf_1", id)
	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, http.MethodGet, got[0].method)
	assert.Equal(t, "/v1/instances/in_000000000001/partner-fees", got[0].path)
}

// fixedClock is a find-or-create clock stopped at the given UTC time.
func fixedClock(a *blindpay.Adapter, at string) {
	instant, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	blindpay.SetClock(a, func() time.Time { return instant })
}

func TestPartnerFeeIsCreatedWithItsNameAndHourAsIdempotencyKey(t *testing.T) {
	client, calls := feeServer(t, `[]`, feeProfile("pf_new", 155, 0))
	a := blindpay.New(nil, nil, nil)
	// A local time east of UTC: the bucket is the UTC hour.
	fixedClock(a, "2026-10-22T11:30:00-03:00")
	id, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)
	assert.Equal(t, "pf_new", id)
	got := calls()
	require.Len(t, got, 2)
	assert.Equal(t, http.MethodPost, got[1].method)
	assert.Equal(t, "xip-fee-in155-out0:2026102214", got[1].idempotency)
	var body map[string]any
	require.NoError(t, json.Unmarshal(got[1].body, &body))
	assert.Equal(t, map[string]any{
		"name": "xip-fee-in155-out0", "payin_percentage_fee": float64(155), "payin_flat_fee": float64(0),
		"payout_percentage_fee": float64(0), "payout_flat_fee": float64(0),
	}, body)
}

func TestPartnerFeeCreateWithoutAnIDFails(t *testing.T) {
	client, _ := feeServer(t, `[]`, `{}`)
	_, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 155, 0)
	assert.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestPartnerFeeIsCachedPerProcess(t *testing.T) {
	client, calls := feeServer(t, `[`+feeProfile("pf_1", 155, 0)+`]`, `{}`)
	a := blindpay.New(nil, nil, nil)
	for range 2 {
		id, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
		require.NoError(t, err)
		assert.Equal(t, "pf_1", id)
	}
	assert.Len(t, calls(), 1)
}

func TestZeroFeesNeedNoProfile(t *testing.T) {
	client, calls := feeServer(t, `[]`, `{}`)
	id, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 0, 0)
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Empty(t, calls())
}

func TestTwoProfilesWithOneNameUseTheFirst(t *testing.T) {
	client, _ := feeServer(t, `[`+feeProfile("pf_a", 0, 50)+`,`+feeProfile("pf_b", 0, 50)+`]`, `{}`)
	id, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 0, 50)
	require.NoError(t, err)
	assert.Equal(t, "pf_a", id)
}

func TestForgetDropsTheCachedID(t *testing.T) {
	client, calls := feeServer(t, `[`+feeProfile("pf_1", 155, 0)+`]`, `{}`)
	a := blindpay.New(nil, nil, nil)
	_, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)
	_, err = blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)
	assert.Len(t, calls(), 1)
	blindpay.ForgetPartnerFee(a, "in_000000000001", 155, 0)
	_, err = blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)
	assert.Len(t, calls(), 2)
}

func TestPartnerFeeCacheIsPerInstance(t *testing.T) {
	a := blindpay.New(nil, nil, nil)
	ids := map[string]string{}
	for _, inst := range []string{"in_000000000001", "in_000000000002"} {
		inst := inst
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "["+feeProfile("pf_"+inst, 155, 0)+"]")
		}))
		t.Cleanup(server.Close)
		client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: inst, BaseURL: server.URL + "/v1"})
		id, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
		require.NoError(t, err)
		ids[inst] = id
	}
	assert.Equal(t, "pf_in_000000000001", ids["in_000000000001"])
	assert.Equal(t, "pf_in_000000000002", ids["in_000000000002"])
}

// replayingFeeServer is BlindPay's create as far as the key goes: a key seen
// before answers the id it first created, for 24h, even after that profile was
// deleted. The list is always empty: the profile is gone from the dashboard.
func replayingFeeServer(t *testing.T) (*blindpay.Client, func() []feeCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []feeCall
	replays := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Idempotency-Key")
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, feeCall{r.Method, r.URL.EscapedPath(), key, body})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		id, ok := replays[key]
		if !ok {
			id = fmt.Sprintf("pf_%012d", len(replays)+1)
			replays[key] = id
		}
		_, _ = io.WriteString(w, feeProfile(id, 155, 0))
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
	return client, func() []feeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]feeCall(nil), calls...)
	}
}

// A profile deleted on the dashboard is evicted on FEES_NOT_FOUND. The create
// that follows in a later hour carries a new key, so BlindPay creates a fresh
// profile instead of replaying the deleted id for the rest of the 24h.
func TestPartnerFeeIsRecreatedAfterAnEvictionInALaterHour(t *testing.T) {
	client, calls := replayingFeeServer(t)
	a := blindpay.New(nil, nil, nil)

	fixedClock(a, "2026-10-22T14:10:00Z")
	first, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)
	blindpay.ForgetPartnerFee(a, "in_000000000001", 155, 0)

	fixedClock(a, "2026-10-22T15:05:00Z")
	second, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "the deleted id is not replayed")
	var keys []string
	for _, call := range calls() {
		if call.method == http.MethodPost {
			keys = append(keys, call.idempotency)
		}
	}
	assert.Equal(t, []string{"xip-fee-in155-out0:2026102214", "xip-fee-in155-out0:2026102215"}, keys)
}

// A profile under XIP's name that charges something else (made on the
// dashboard, say) is never used: the estimate fails and an operator is told.
// Nothing is created beside it either.
func TestPartnerFeeUnderXIPsNameChargingOtherFeesIsRefused(t *testing.T) {
	cases := map[string]string{
		"another payin percentage":  `{"id":"pf_bad","name":"xip-fee-in155-out0","payin_percentage_fee":300,"payin_flat_fee":0,"payout_percentage_fee":0,"payout_flat_fee":0}`,
		"a payin flat fee":          `{"id":"pf_bad","name":"xip-fee-in155-out0","payin_percentage_fee":155,"payin_flat_fee":100,"payout_percentage_fee":0,"payout_flat_fee":0}`,
		"another payout percentage": `{"id":"pf_bad","name":"xip-fee-in155-out0","payin_percentage_fee":155,"payin_flat_fee":0,"payout_percentage_fee":50,"payout_flat_fee":0}`,
		"a payout flat fee":         `{"id":"pf_bad","name":"xip-fee-in155-out0","payin_percentage_fee":155,"payin_flat_fee":0,"payout_percentage_fee":0,"payout_flat_fee":1}`,
		"no fees named":             `{"id":"pf_bad","name":"xip-fee-in155-out0"}`,
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay partner fee %s is named %s but charges other fees",
				"pf_bad", "xip-fee-in155-out0").Once()
			client, calls := feeServer(t, "["+profile+"]", feeProfile("pf_new", 155, 0))

			id, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 155, 0)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.Empty(t, id)
			assert.Len(t, calls(), 1, "no profile is created beside it")
			logger.AssertExpectations(t)
		})
	}
}

// One right profile beside a wrong one under the same name is the one used.
func TestPartnerFeeUsesTheProfileThatChargesWhatWasAskedFor(t *testing.T) {
	wrong := `{"id":"pf_bad","name":"xip-fee-in155-out0","payin_percentage_fee":300,"payin_flat_fee":0,"payout_percentage_fee":0,"payout_flat_fee":0}`
	client, _ := feeServer(t, "["+wrong+","+feeProfile("pf_1", 155, 0)+"]", `{}`)
	id, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 155, 0)
	require.NoError(t, err)
	assert.Equal(t, "pf_1", id)
}

// A create answered with other fees than were sent is not trusted either.
func TestPartnerFeeCreatedWithOtherFeesIsRefused(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay partner fee %s was created charging other fees than %s",
		"pf_new", "xip-fee-in155-out0").Once()
	client, _ := feeServer(t, `[]`, feeProfile("pf_new", 15500, 0))
	_, err := blindpay.PartnerFeeID(blindpay.New(nil, nil, nil), context.Background(), client, 155, 0)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	logger.AssertExpectations(t)
}

// slowFeeServer is feeServer whose list answers after a pause, so concurrent
// first callers overlap in the find-or-create.
func slowFeeServer(t *testing.T, list, created string) (*blindpay.Client, func() []feeCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []feeCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, feeCall{r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			time.Sleep(100 * time.Millisecond)
			_, _ = io.WriteString(w, list)
			return
		}
		_, _ = io.WriteString(w, created)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
	return client, func() []feeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]feeCall(nil), calls...)
	}
}

// concurrently runs n calls of f at once and returns their answers.
func concurrently(n int, f func() (string, error)) ([]string, []error) {
	ids, errs := make([]string, n), make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids[i], errs[i] = f()
		}()
	}
	close(start)
	wg.Wait()
	return ids, errs
}

// Concurrent first estimates share one find-or-create: one list, one create,
// one id. Separate POSTs under one key would get BlindPay's 409
// idempotency_key_in_flight for all but one of them.
func TestPartnerFeeConcurrentFirstCallsMakeOneCreate(t *testing.T) {
	client, calls := slowFeeServer(t, `[]`, feeProfile("pf_new", 155, 0))
	a := blindpay.New(nil, nil, nil)

	ids, errs := concurrently(8, func() (string, error) {
		return blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
	})
	for i := range ids {
		require.NoError(t, errs[i])
		assert.Equal(t, "pf_new", ids[i])
	}
	got := calls()
	require.Len(t, got, 2, "%v", got)
	assert.Equal(t, http.MethodGet, got[0].method)
	assert.Equal(t, http.MethodPost, got[1].method)
}

// The first caller giving up must not fail the callers sharing its flight.
func TestPartnerFeeACancelledFirstCallerDoesNotFailTheOthers(t *testing.T) {
	client, _ := slowFeeServer(t, `[]`, feeProfile("pf_new", 155, 0))
	a := blindpay.New(nil, nil, nil)

	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := blindpay.PartnerFeeID(a, first, client, 155, 0)
		firstErr <- err
	}()
	time.Sleep(30 * time.Millisecond)
	secondID := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		id, err := blindpay.PartnerFeeID(a, context.Background(), client, 155, 0)
		secondID <- id
		secondErr <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	require.ErrorIs(t, <-firstErr, context.Canceled)
	require.NoError(t, <-secondErr)
	assert.Equal(t, "pf_new", <-secondID)
}
