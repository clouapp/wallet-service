package blindpay_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

// TestMain binds the log facade once for the package: Do reports latency and
// outcome on every call, so every test here touches the logger.
func TestMain(m *testing.M) {
	mock.Factory().Log()
	os.Exit(m.Run())
}

type recorded struct {
	method, path, rawQuery, auth, idempotency, contentType string
	body                                                   []byte
}

func newTestClient(t *testing.T, status int, response string) (*blindpay.Client, *recorded) {
	t.Helper()
	rec := &recorded{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.rawQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		rec.auth, rec.idempotency = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		rec.contentType = r.Header.Get("Content-Type")
		rec.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1",
	})
	return client, rec
}

func TestClient_ScopesEveryPathToTheInstance(t *testing.T) {
	client, rec := newTestClient(t, 200, `{"ok":true}`)
	var out map[string]any
	err := client.Do(context.Background(), blindpay.Request{
		Method: http.MethodGet, Path: "customers", Query: url.Values{"limit": {"1"}},
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, "/v1/instances/in_000000000001/customers", rec.path)
	assert.Equal(t, "limit=1", rec.rawQuery)
	assert.Equal(t, "Bearer bp_test_key", rec.auth)
	assert.Equal(t, true, out["ok"])
}

func TestClient_ExternalScopeIsTheEPrefix(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	err := client.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "tos", Scope: blindpay.ScopeExternal, Body: map[string]string{"a": "b"},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "/v1/e/instances/in_000000000001/tos", rec.path)
	assert.Equal(t, "application/json", rec.contentType)
	assert.JSONEq(t, `{"a":"b"}`, string(rec.body))
}

// A path segment is escaped, never interpreted: an id carrying a slash or a
// dot-dot cannot climb out of the instance, and the refusal happens before a
// request — and the API key in it — ever leaves.
func TestClient_RefusesAPathThatCouldLeaveTheInstance(t *testing.T) {
	for _, path := range []string{
		"", "/customers", "customers/", "customers//x", "customers/../payouts",
		"customers/.", "customers/a?b", "customers/a#b", `customers/a\b`,
	} {
		client, rec := newTestClient(t, 200, `{}`)
		err := client.Do(context.Background(), blindpay.Request{Method: http.MethodGet, Path: path}, nil)
		assert.ErrorIs(t, err, ramp.ErrRequestNotSent, "path %q", path)
		assert.Empty(t, rec.method, "path %q must not reach the server", path)
	}
}

func TestClient_EscapesEachSegment(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	err := client.Do(context.Background(), blindpay.Request{Method: http.MethodGet, Path: "customers/re 1"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "/v1/instances/in_000000000001/customers/re%201", rec.path)
}

func TestClient_SendsTheIdempotencyKey(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	require.NoError(t, client.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "quotes", Body: map[string]int{"x": 1}, IdempotencyKey: "kyc-app:42",
	}, nil))
	assert.Equal(t, "kyc-app:42", rec.idempotency)
}

// BlindPay caps the key at 255 characters. A longer one is replaced by a
// digest rather than truncated, so two long keys sharing a prefix stay apart
// and a retry still sends the same header.
func TestClient_DigestsAnOverlongIdempotencyKey(t *testing.T) {
	long := strings.Repeat("k", 300)
	client, rec := newTestClient(t, 200, `{}`)
	require.NoError(t, client.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "quotes", IdempotencyKey: long,
	}, nil))
	first := rec.idempotency
	assert.LessOrEqual(t, len(first), 255)
	assert.True(t, strings.HasPrefix(first, "sha256-"))

	client2, rec2 := newTestClient(t, 200, `{}`)
	require.NoError(t, client2.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "quotes", IdempotencyKey: long,
	}, nil))
	assert.Equal(t, first, rec2.idempotency, "the replacement is deterministic")
}

func TestClient_A2xxThatIsNotJSONIsTheProvidersFailure(t *testing.T) {
	client, _ := newTestClient(t, 200, `<html>`)
	var out map[string]any
	err := client.Do(context.Background(), blindpay.Request{Method: http.MethodGet, Path: "customers"}, &out)
	assert.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestClient_InstanceID(t *testing.T) {
	client, _ := newTestClient(t, 200, `{}`)
	assert.Equal(t, "in_000000000001", client.InstanceID())
}

func TestClient_BodyIsEncodedDeterministically(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	body := map[string]any{"b": 2, "a": 1}
	require.NoError(t, client.Do(context.Background(), blindpay.Request{Method: http.MethodPost, Path: "quotes", Body: body}, nil))
	want, _ := json.Marshal(body)
	assert.Equal(t, string(want), string(rec.body), "BlindPay compares retries by the SHA-256 of the bytes")
}

// A raw body travels as the exact bytes given — key order, spacing and all —
// because a retried create is matched by the hash of those bytes.
func TestClient_RawBodyIsSentByteForByte(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	raw := []byte(`{"z":1,  "a":"<b>"}`)
	require.NoError(t, client.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "customers", RawBody: raw, IdempotencyKey: "k",
	}, nil))
	assert.Equal(t, raw, rec.body)
	assert.Equal(t, "application/json", rec.contentType)
}

func TestClient_RefusesBodyAndRawBodyTogether(t *testing.T) {
	client, rec := newTestClient(t, 200, `{}`)
	err := client.Do(context.Background(), blindpay.Request{
		Method: http.MethodPost, Path: "customers", Body: map[string]int{"a": 1}, RawBody: []byte(`{}`),
	}, nil)
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
	assert.Empty(t, rec.method, "nothing reached the server")
}

func TestClient_CarriesTheProviderMode(t *testing.T) {
	assert.Equal(t, "sandbox", blindpay.NewClient(blindpay.Config{Mode: "sandbox"}).Mode())
}
