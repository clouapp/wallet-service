package blindpay_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

func tosReply(key string) string {
	return `{"url":"https://app.blindpay.com/e/terms-of-service?session_token=tok&idempotency_key=` + key + `"}`
}

func TestCreateTosSession_SendsTheSessionRequest(t *testing.T) {
	key := uuid.NewString()
	client, rec := newTestClient(t, 200, tosReply(key))
	adapter := blindpay.New(clientFactory(client, nil), nil, nil)

	session, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{CustomerID: 1},
		ramp.TosOpts{RedirectURI: "xip://tos", IdempotencyKey: key})
	require.NoError(t, err)
	assert.Equal(t, "/v1/e/instances/in_000000000001/tos", rec.path)
	assert.Empty(t, rec.idempotency)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.body, &body))
	assert.Equal(t, key, body["idempotency_key"])
	assert.Equal(t, "xip://tos", body["redirect_url"])
	assert.NotContains(t, body, "customer_id")
	assert.Equal(t, key, session.SessionID)
	assert.Empty(t, session.SignedAgreementID)
	assert.Equal(t, []string{"app.blindpay.com"}, session.AllowedHosts)
	assert.Contains(t, session.URL, "session_token=tok")
}

func TestCreateTosSession_NamesTheCustomerWhenThereIsOne(t *testing.T) {
	// The key is not a UUID, so the adapter derives one; the reply must echo it.
	derived := uuid.NewSHA1(uuid.NameSpaceURL, []byte("blindpay:tos:abc")).String()
	client, rec := newTestClient(t, 200, tosReply(derived))
	adapter := blindpay.New(clientFactory(client, nil), nil, nil)

	session, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"},
		ramp.TosOpts{IdempotencyKey: "abc"})
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.body, &body))
	assert.Equal(t, "cu_1", body["customer_id"])
	assert.Equal(t, derived, body["idempotency_key"])
	assert.NotContains(t, body, "redirect_url")
	assert.Equal(t, derived, session.SessionID)
}

func TestCreateTosSession_RefusesAnAnswerItCannotTrust(t *testing.T) {
	key := uuid.NewString()
	cases := map[string]string{
		"other host":  `{"url":"https://evil.example/e/terms-of-service?session_token=t&idempotency_key=` + key + `"}`,
		"other path":  `{"url":"https://app.blindpay.com/other?session_token=t&idempotency_key=` + key + `"}`,
		"no token":    `{"url":"https://app.blindpay.com/e/terms-of-service?idempotency_key=` + key + `"}`,
		"other key":   tosReply(uuid.NewString()),
		"http scheme": `{"url":"http://app.blindpay.com/e/terms-of-service?session_token=t&idempotency_key=` + key + `"}`,
		"no url":      `{}`,
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := newTestClient(t, 200, response)
			adapter := blindpay.New(clientFactory(client, nil), nil, nil)
			_, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{}, ramp.TosOpts{IdempotencyKey: key})
			assert.ErrorIs(t, err, ramp.ErrProviderFailed)
		})
	}
}

func TestCreateTosSession_RefusesARedirectBeforeSending(t *testing.T) {
	for _, redirect := range []string{"http://x", "https://x/#frag", "https://u:p@x/", "/relative", "ftp://x"} {
		t.Run(redirect, func(t *testing.T) {
			adapter, hits := routedServer(t, nil)
			_, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{},
				ramp.TosOpts{RedirectURI: redirect, IdempotencyKey: uuid.NewString()})
			require.Error(t, err)
			assert.Zero(t, hits.Load())
		})
	}
}
