package ramp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/tests/support/closedport"
)

// The core safety property: a failure before the bytes left is the only one
// the router may fail over from on a create.
func TestSendMarksARefusedConnectionAsNotSent(t *testing.T) {
	addr := closedport.Addr(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+addr+"/customers", nil)
	require.NoError(t, err)

	_, _, err = Send(&http.Client{Timeout: time.Second}, req)

	require.ErrorIs(t, err, ErrRequestNotSent)
	require.ErrorIs(t, err, ErrProviderFailed)
}

// A transport failure's *url.Error restates the whole URL, query included, and
// LogUpstreamCall logs err.Error(): Bridge's duplicate-e-mail recovery looks the
// customer up with ?email=.
func TestSendKeepsTheQueryOutOfATransportError(t *testing.T) {
	addr := closedport.Addr(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+addr+"/customers?email=victim%40x.com", nil)
	require.NoError(t, err)

	_, _, err = Send(&http.Client{Timeout: time.Second}, req)

	require.ErrorIs(t, err, ErrRequestNotSent)
	require.NotContains(t, err.Error(), "victim")
}

// A request that was sent and then timed out is unknown state. Marking it
// not-sent would let the router place the same order on a second provider.
func TestSendDoesNotMarkATimeoutAsNotSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/customers", nil)
	require.NoError(t, err)

	_, _, err = Send(&http.Client{Timeout: 50 * time.Millisecond}, req)

	require.ErrorIs(t, err, ErrProviderFailed)
	require.NotErrorIs(t, err, ErrRequestNotSent,
		"a create that timed out must never be retried on another provider")
}

// A rejection is an answer, not a transport failure: the adapter classifies it
// against its own provider's envelope.
func TestSendHandsARejectionBackToTheCaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters"}`))
	}))
	t.Cleanup(server.Close)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, body, err := Send(server.Client(), req)

	require.NoError(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Equal(t, "7", resp.Header.Get("Retry-After"))
	require.JSONEq(t, `{"code":"invalid_parameters"}`, string(body))
}

type decodedCustomer struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type fakeRequest struct{ path string }

func TestCallDecodesTheAnswerAndKeepsItsBytes(t *testing.T) {
	var asked fakeRequest
	do := func(_ context.Context, req fakeRequest, out any) error {
		asked = req
		return json.Unmarshal([]byte(`{"id":"cus_1","status":"active","extra":true}`), out)
	}

	customer, raw, err := Call[decodedCustomer](context.Background(), do, fakeRequest{path: "/customers/cus_1"})

	require.NoError(t, err)
	require.Equal(t, fakeRequest{path: "/customers/cus_1"}, asked)
	require.Equal(t, decodedCustomer{ID: "cus_1", Status: "active"}, customer)
	require.JSONEq(t, `{"id":"cus_1","status":"active","extra":true}`, string(raw),
		"the untouched body is what the services persist as raw_status")
}

func TestCallReturnsTheTransportErrorUntouched(t *testing.T) {
	refused := errors.New("bridge: GET /customers/cus_1: upstream rejected (http_404)")
	do := func(context.Context, fakeRequest, any) error { return refused }

	_, raw, err := Call[decodedCustomer](context.Background(), do, fakeRequest{})

	require.Same(t, refused, err)
	require.Nil(t, raw)
}

func TestCallNamesTheTypeABodyDidNotDecodeInto(t *testing.T) {
	do := func(_ context.Context, _ fakeRequest, out any) error {
		return json.Unmarshal([]byte(`{"id":42}`), out)
	}

	_, raw, err := Call[decodedCustomer](context.Background(), do, fakeRequest{})

	require.ErrorContains(t, err, "decode ramp.decodedCustomer")
	require.ErrorIs(t, err, ErrProviderFailed, "a 2xx this side cannot read is the provider's failure: 502, not 500")
	require.Nil(t, raw)
}
