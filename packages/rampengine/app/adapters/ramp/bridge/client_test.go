package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second}), server
}

func TestDoSendsAuthAndIdempotencyHeaders(t *testing.T) {
	var gotAPIKey, gotIdempotency, gotContentType string
	var gotBody map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("Api-Key")
		gotIdempotency = r.Header.Get("Idempotency-Key")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cus_1"}`))
	})

	var out struct {
		ID string `json:"id"`
	}
	err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		Path:           "/customers",
		Body:           map[string]string{"email": "a@x.com"},
		IdempotencyKey: "derived-key",
	}, &out)

	require.NoError(t, err)
	require.Equal(t, "sk_test", gotAPIKey)
	require.Equal(t, "derived-key", gotIdempotency, "every mutation must carry the derived key")
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "a@x.com", gotBody["email"])
	require.Equal(t, "cus_1", out.ID)
}

func TestDoOmitsIdempotencyHeaderWhenUnset(t *testing.T) {
	var present bool
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Idempotency-Key"]
		_, _ = w.Write([]byte(`{}`))
	})

	require.NoError(t, client.Do(context.Background(), Request{
		Method: http.MethodGet, Path: "/customers/cus_1",
	}, nil))
	require.False(t, present, "a read must not claim idempotency it does not have")
}

// The core safety property: a failure before the bytes left is the only one the
// router may fail over from on a create.
func TestDoMarksConnectionRefusedAsNotSent(t *testing.T) {
	addr := closedport.Addr(t)

	client := NewClient(Config{BaseURL: "http://" + addr, APIKey: "sk_test", Timeout: time.Second})
	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// A request that was sent and then timed out is unknown state. Marking it
// not-sent would let the router place the same order on a second provider.
func TestDoDoesNotMarkTimeoutAsNotSent(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	})
	// Rebuild with a timeout shorter than the handler's sleep.
	client = NewClient(Config{BaseURL: client.cfg.BaseURL, APIKey: "sk", Timeout: 50 * time.Millisecond})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrRequestNotSent,
		"a create that timed out must never be retried on another provider")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// ErrProviderFailed is what lets the HTTP layer answer 502 instead of 500, so
// every answer Bridge gives that is not a usable 2xx has to carry it —
// including the rejections that also name a sentinel of their own.
func TestDoMarksEveryUnusableAnswerAsTheProviders(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"a 5xx with no envelope":   {http.StatusInternalServerError, `{}`},
		"a rejection with a code":  {http.StatusBadRequest, `{"code":"invalid_parameters"}`},
		"a refused key":            {http.StatusUnauthorized, `{"code":"unauthorized"}`},
		"a throttle with no body":  {http.StatusTooManyRequests, ``},
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

// A request this side could not even encode never reached Bridge, and blaming
// Bridge for it would hide our own bug behind a provider outage.
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

// A throttle with an empty body is the one post-send case that is provably
// inert: nothing was created upstream.
func TestDoTreatsEmptyThrottleAsNotSent(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
}

func TestDoThrottleWithBodyIsNotRetryable(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"rate_limited"}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrRequestNotSent)
}

// Bridge echoes submitted customer fields in validation errors. Those carry
// PII, so the message must never reach our error string.
func TestDoErrorOmitsResponseBody(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"tax_id 39053344705 is invalid"}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid_parameters")
	require.NotContains(t, err.Error(), "39053344705")

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "invalid_parameters", upstreamErr.Code)
}

func TestDoClassifiesDuplicateEmailByCode(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"email already exists"}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "duplicate_email", upstreamErr.Code)
	require.NotContains(t, err.Error(), "already exists")
}

func TestDoClassifiesDuplicateEmailBySourceKey(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","source":{"key":"email"},"message":"conflict"}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)
	require.Error(t, err)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "duplicate_email", upstreamErr.Code)
	require.NotContains(t, err.Error(), "conflict")
}

// One field of a type the envelope does not expect fails the whole decode. The
// code is still read on its own: production answered 16/09 with a 422 that
// reached the log only as http_422, and nothing else said why.
func TestDoReadsTheCodeWhenAnotherFieldDoesNotDecode(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","source":{"key":["pix_key"]},"message":"39053344705"}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "invalid_parameters", upstreamErr.Code)
	require.NotContains(t, err.Error(), "39053344705")
}

// With no code at all, the names of the fields Bridge did send are what is left
// to go on. Names only: the values can restate the customer's data.
func TestDoNamesTheBodyFieldsWhenThereIsNoCode(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"pix key 39053344705 is invalid","errors":{"pix_key":"bad"}}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "http_422", upstreamErr.Code)
	require.Contains(t, err.Error(), "body keys: errors,message")
	require.NotContains(t, err.Error(), "39053344705")
	require.NotContains(t, err.Error(), "bad")
}

func TestDoNamesNoBodyFieldsWhenTheBodyIsNotAnObject(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`<html>unprocessable</html>`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	require.EqualError(t, err, "bridge: POST /customers: upstream rejected (http_422)")
}

func TestBaseURLForMode(t *testing.T) {
	sandbox, err := BaseURLForMode(models.ProviderModeSandbox)
	require.NoError(t, err)
	require.Equal(t, SandboxBaseURL, sandbox)

	production, err := BaseURLForMode(models.ProviderModeProduction)
	require.NoError(t, err)
	require.Equal(t, ProductionBaseURL, production)

	_, err = BaseURLForMode("staging")
	require.Error(t, err)
}

// cmd/devstub shares one *http.Client across every factory call, so the
// client dials through it but never writes to it, and still gets a timeout.
func TestNewClientDialsThroughASuppliedHTTPClientWithoutChangingIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	shared := server.Client()
	require.Zero(t, shared.Timeout)

	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk", HTTPClient: shared})

	require.NoError(t, client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/customers/cus_1"}, nil),
		"a self-signed server is reachable only through the supplied transport")
	require.Zero(t, shared.Timeout, "the caller's client must not be mutated")
	require.Equal(t, defaultTimeout, client.http.Timeout, "an unbounded client would defeat the slow scenario")
}

// The sandbox answers a second customer under one e-mail (a person's and a
// business's alike, 18/10/2026) with source.key as an object naming the field
// and why. Read as a string, the whole envelope failed to decode and the
// duplicate went out as invalid_parameters: the person's recovery never ran,
// and a company's e-mail in use was a provider failure.
func TestDoClassifiesDuplicateEmailByAnObjectSourceKey(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"Please resubmit the following parameters that are either missing or invalid","source":{"location":"body","key":{"email":"A customer with this email already exists"}}}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "duplicate_email", upstreamErr.Code)
	require.NotContains(t, err.Error(), "already exists")
}

// Another invalid field stays invalid_parameters.
func TestDoKeepsAnotherInvalidParameterAsItIs(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"Please resubmit","source":{"location":"body","key":{"phone":"is invalid"}}}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "/customers"}, nil)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "invalid_parameters", upstreamErr.Code)
}

// A refusal of parameters names the fields it refused, for the admin, by
// name only: Bridge's reason beside each may restate a value.
func TestDoNamesTheRefusedFieldsAndNotWhy(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"Please resubmit","source":{"location":"body","key":{"liveness_check_selfies":"needs a PIX endorsement","identifying_information":"bad"}}}`))
	})

	err := client.Do(context.Background(), Request{Method: http.MethodPut, Path: "/customers/cus_1"}, nil)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, []string{"identifying_information", "liveness_check_selfies"}, upstreamErr.Fields)
	require.Contains(t, err.Error(), "invalid_parameters: identifying_information, liveness_check_selfies")
	require.NotContains(t, err.Error(), "PIX endorsement")
}
