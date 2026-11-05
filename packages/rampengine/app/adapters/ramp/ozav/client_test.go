package ozav

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/closedport"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second, BaseURL: server.URL}), server
}

func TestDoSendsAPIKeyAndIdempotencyHeaders(t *testing.T) {
	var gotAPIKey, gotIdempotency, gotContentType string
	var gotBody map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-Key")
		gotIdempotency = r.Header.Get("Idempotency-Key")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1"}`))
	})

	var out struct {
		ID string `json:"id"`
	}
	err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		Path:           "/customers",
		Body:           map[string]string{"email": "a@x.com"},
		IdempotencyKey: "derived-key-1234567890",
	}, &out)

	require.NoError(t, err)
	require.Equal(t, "ozav_sk_test_abc123", gotAPIKey, "X-API-Key must be sent on every call")
	require.Equal(t, "derived-key-1234567890", gotIdempotency, "every mutation must carry the derived key")
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "a@x.com", gotBody["email"])
	require.Equal(t, "ozav_cus_1", out.ID)
}

func TestDoOmitsIdempotencyHeaderWhenUnset(t *testing.T) {
	var present bool
	var gotAPIKey string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Idempotency-Key"]
		gotAPIKey = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{}`))
	})

	require.NoError(t, client.Do(context.Background(), Request{
		Method: http.MethodGet, Path: "/customers/ozav_cus_1",
	}, nil))
	require.False(t, present, "a read must not claim idempotency it does not have")
	require.Equal(t, "ozav_sk_test_abc123", gotAPIKey, "X-API-Key must be sent even on a plain read")
}

func TestValidateKeyMode(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		mode    string
		wantErr bool
	}{
		{"test key in sandbox", "ozav_sk_test_abc123", models.ProviderModeSandbox, false},
		{"live key in production", "ozav_sk_live_abc123", models.ProviderModeProduction, false},
		{"test key in production", "ozav_sk_test_abc123", models.ProviderModeProduction, true},
		{"live key in sandbox", "ozav_sk_live_abc123", models.ProviderModeSandbox, true},
		{"unrecognized prefix", "sk_live_abc123", models.ProviderModeProduction, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKeyMode(tt.apiKey, tt.mode)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// The core safety property: a failure before the bytes left is the only one
// the router may fail over from on a create.
func TestDoMarksConnectionRefusedAsNotSent(t *testing.T) {
	addr := closedport.Addr(t)

	client := NewClient(Config{APIKey: "ozav_sk_test_abc123", Timeout: time.Second, BaseURL: "http://" + addr})
	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// A request that was sent and then timed out is unknown state. Marking it
// not-sent would let the router place the same transfer on a second
// provider. This is the test that must fail loudly if that rule is ever
// inverted: it asserts the ABSENCE of the sentinel, not its presence.
func TestDoDoesNotMarkTimeoutAsNotSent(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	})
	// Rebuild with a timeout shorter than the handler's sleep, same server.
	client = NewClient(Config{APIKey: client.cfg.APIKey, Timeout: 50 * time.Millisecond, BaseURL: client.baseURL})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrRequestNotSent,
		"a create that timed out must never be retried on another provider")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// ErrProviderFailed is what lets the HTTP layer answer 502 instead of 500, so
// every answer Ozav gives that is not a usable 2xx has to carry it — including
// the rejections that also name a sentinel of their own.
func TestDoMarksEveryUnusableAnswerAsTheProviders(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"a 5xx with no envelope":   {http.StatusInternalServerError, `{}`},
		"a classified rejection":   {http.StatusUnprocessableEntity, `{"error":{"type":"invalid_request_error","code":"QUOTE_EXPIRED"}}`},
		"an unclassified one":      {http.StatusBadRequest, `{"error":{"type":"invalid_request_error","code":"some_error"}}`},
		"a refused key":            {http.StatusUnauthorized, `{"error":{"type":"authentication_error","code":"INVALID_API_KEY"}}`},
		"a 2xx that is not JSON":   {http.StatusOK, `<html>maintenance</html>`},
		"a rejection that is HTML": {http.StatusBadGateway, `<html>bad gateway</html>`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			var out struct{ ID string }
			err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, &out)

			require.ErrorIs(t, err, ramp.ErrProviderFailed)
		})
	}
}

// The upload is a second wire to Ozav's storage, and it fails the same two
// ways the API does.
func TestUploadMarksAFailedPutAsTheProviders(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"a 5xx": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
		"a dropped connection": func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(handler)
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			client := NewClient(Config{
				APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second,
				HTTPClient: server.Client(), UploadHosts: []string{target.Hostname()},
			})

			err = client.Upload(context.Background(), server.URL+"/uploads/doc_1", "image/png", []byte("png"), nil)

			require.ErrorIs(t, err, ramp.ErrProviderFailed)
		})
	}
}

// A request this side could not even encode never reached Ozav, and blaming
// Ozav for it would hide our own bug behind a provider outage.
func TestDoKeepsALocalFailureOffTheProvider(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	err := client.Do(context.Background(), Request{
		Method: http.MethodPost, Path: "/customers", Body: map[string]any{"unencodable": make(chan int)},
	}, nil)

	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrProviderFailed)
}

// The KYC service mints idempotency keys as "kyc-app:<id>" /
// "kyc-docs:<id>" for every provider alike (see
// ProviderKycService.send) — correct from XIP's side, but the colon falls
// outside Ozav's own contract for Idempotency-Key: ^[A-Za-z0-9_-]{16,256}$.
// Confirmed against the sandbox: the same call, colon swapped for a hyphen,
// moves from INVALID_IDEMPOTENCY_KEY to accepted. The assertion is on the
// header actually sent, not just on the normalizer function, because a
// normalizer that is correct but never wired into Do would fail exactly the
// same way this bug did.
func TestDoNormalizesColonInIdempotencyKeyForTheWire(t *testing.T) {
	var gotIdempotency string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIdempotency = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1"}`))
	})

	err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		Path:           "/customers",
		IdempotencyKey: "kyc-app:3e2c901eab60c43902c59f6aa5071c16",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "kyc-app-3e2c901eab60c43902c59f6aa5071c16", gotIdempotency,
		"the colon the service format carries must become a hyphen on the wire")
}

// Normalization must be deterministic: the whole point of an idempotency key
// is that a retry produces the same one, and a normalizer that is not stable
// would defeat that guarantee even though it never touches the network.
func TestNormalizeIdempotencyKeyIsDeterministic(t *testing.T) {
	const key = "kyc-app:3e2c901eab60c43902c59f6aa5071c16"
	first := normalizeIdempotencyKey(key)
	second := normalizeIdempotencyKey(key)
	require.Equal(t, first, second)
}

// A key that already satisfies Ozav's contract must reach the wire
// untouched — normalization exists to accommodate the one format the
// service is known to produce, not to reshape every key that passes
// through here.
func TestDoLeavesAnAlreadyValidIdempotencyKeyUnchanged(t *testing.T) {
	var gotIdempotency string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIdempotency = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1"}`))
	})

	const validKey = "already-valid-key-1234"
	err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		Path:           "/customers",
		IdempotencyKey: validKey,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, validKey, gotIdempotency)
}

// A key below Ozav's 16-character floor used to be refused here. It is now
// extended instead, and this is the behavioural half of that change: the
// request reaches the wire, carrying a header Ozav's own pattern accepts.
func TestDoExtendsAShortIdempotencyKeyAndStillSends(t *testing.T) {
	var gotIdempotency string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIdempotency = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{}`))
	})

	err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		Path:           "/customers",
		IdempotencyKey: "kyc-link:3:ozav",
	}, nil)

	require.NoError(t, err)
	require.GreaterOrEqual(t, len(gotIdempotency), minIdempotencyKeyLen)
	require.True(t, idempotencyKeyPattern.MatchString(gotIdempotency), "got %q", gotIdempotency)
}

// The discriminating part of every key this repo derives (kyc-app:<id>,
// kyc-docs:<id>) sits at the end. Truncating a too-long key from the front
// keeps that part intact; truncating from the back would collide distinct
// submissions onto the same header.
func TestNormalizeIdempotencyKeyTruncatesFromTheFrontKeepingTheSuffix(t *testing.T) {
	suffix := strings.Repeat("x", maxIdempotencyKeyLen)
	long := strings.Repeat("a", 50) + suffix

	got := normalizeIdempotencyKey(long)

	require.Len(t, got, maxIdempotencyKeyLen)
	require.Equal(t, suffix, got, "the tail — where this repo's keys carry their unique id — must survive truncation")
}

// Any response that reached us, success or not, means Ozav did work with the
// request. Neither a 4xx nor a 5xx may be treated as unsent.
func TestDoNonSuccessWithBodyIsNotRetryable(t *testing.T) {
	statuses := []int{http.StatusBadRequest, http.StatusInternalServerError}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"some_error","message":"x"}}`))
			})

			err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
			require.Error(t, err)
			require.NotErrorIs(t, err, ramp.ErrRequestNotSent)
		})
	}
}

// A key the service derives can land just under Ozav's floor: the hosted
// link's is "kyc-link:<userID>:ozav", which for a single-digit user id
// normalizes to 15 characters — one short of 16. Refusing it fails a call
// that is perfectly legitimate, and it fails it locally, so the user sees a
// provider error for a naming rule they have nothing to do with. Measured
// against the sandbox on 28/08: every hosted-link request for user 3 died
// here without a byte going out.
//
// The floor is met by extending the key deterministically, never by padding
// it with anything arbitrary — the whole value of an idempotency key is that
// the same call produces the same one.
func TestNormalizeIdempotencyKeyExtendsAKeyBelowTheFloor(t *testing.T) {
	got := normalizeIdempotencyKey("kyc-link:3:ozav")

	require.GreaterOrEqual(t, len(got), minIdempotencyKeyLen)
	require.True(t, idempotencyKeyPattern.MatchString(got), "the result must satisfy Ozav's own pattern: %q", got)
	require.Contains(t, got, "kyc-link-3-ozav", "the readable key stays readable in a support ticket")
}

func TestNormalizeIdempotencyKeyExtendsTheSameKeyTheSameWay(t *testing.T) {
	first := normalizeIdempotencyKey("kyc-link:3:ozav")
	second := normalizeIdempotencyKey("kyc-link:3:ozav")

	require.Equal(t, first, second, "a retry of the same call must collide with itself")
}

// And two different short keys must not be extended onto the same value —
// that would make two distinct calls one replayed request.
func TestNormalizeIdempotencyKeyKeepsShortKeysDistinct(t *testing.T) {
	a := normalizeIdempotencyKey("kyc-link:3:ozav")
	b := normalizeIdempotencyKey("kyc-link:4:ozav")

	require.NotEqual(t, a, b)
}

// The upload_url comes out of Ozav's own create-document answer, so one this
// side cannot even parse is Ozav's response being unusable — the one provider
// failure a decodable 2xx still carries. Left unmarked it answered 500 and
// told the app nothing was wrong with the provider.
func TestUploadMarksAnUnusableUploadURLAsTheProviders(t *testing.T) {
	client := NewClient(Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second})

	err := client.Upload(context.Background(), "://uploads/doc_1", "image/png", []byte("png"), nil)

	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	// Nothing was dialed, so the create may still fail over to another
	// provider rather than stalling on an unknown state.
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
}

// cmd/devstub shares one *http.Client across every factory call, so the
// client dials through it but never writes to it, and still gets a timeout.
func TestNewClientDialsThroughASuppliedHTTPClientWithoutChangingIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	shared := server.Client()

	client := NewClient(Config{APIKey: "ozav_sk_test_abc123", BaseURL: server.URL, HTTPClient: shared})

	require.NoError(t, client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/customers/c"}, nil))
	require.Zero(t, shared.Timeout, "the caller's client must not be mutated")
	require.Equal(t, defaultTimeout, client.http.Timeout)
}

// Production names no base URL and no upload hosts: it gets Ozav's own.
func TestNewClientDefaultsToOzavsHostAndUploadHosts(t *testing.T) {
	client := NewClient(Config{APIKey: "ozav_sk_live_abc123"})

	require.Equal(t, BaseURL, client.baseURL)
	require.Equal(t, uploadHosts, client.uploadHosts)
}
