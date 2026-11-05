package ozav

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// Ozav accepts terms in our flow, not on a page it hosts.
var _ ramp.TosAttester = (*Adapter)(nil)

func TestRequiresTosIsTrueNowThatSessionsCanBeOpened(t *testing.T) {
	require.True(t, (&Adapter{}).RequiresTos())
}

// The create takes no body at all, and deliberately no Idempotency-Key: a
// replayed key answers 200 *without* the token, and the token is issued once.
// Replaying is therefore how the session would be lost, while a duplicate
// pending session costs nothing.
func TestCreateTosSessionPostsWithNeitherBodyNorIdempotencyKey(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "ts_8Kd2mQ4vR7xN1pL5wT3yB6zA9cF0hJ2s",
			"object": "terms_session",
			"status": "pending",
			"terms_url": "https://ozav.io/terms",
			"terms_version": "2.0",
			"expires_at": "2026-09-03T18:00:00Z"
		}`))
	})

	session, err := adapter.CreateTosSession(context.Background(), ramp.NewCustomerRef(7, nil),
		ramp.TosOpts{RedirectURI: "https://app.xip.cash/tos", IdempotencyKey: "kyc-tos-7-ozav"})

	require.NoError(t, err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers/terms-sessions", got.path)
	require.Empty(t, got.body)
	require.Empty(t, got.header.Get("Idempotency-Key"))

	require.Equal(t, "ts_8Kd2mQ4vR7xN1pL5wT3yB6zA9cF0hJ2s", session.SessionID)
	require.Equal(t, "https://ozav.io/terms", session.URL)
	require.Equal(t, time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC), session.ExpiresAt.UTC())
	// Nothing is signed yet. Reporting an agreement id here is what would let
	// an unaccepted session pass the submission gate.
	require.Empty(t, session.SignedAgreementID)
}

// A 200 without the token is Ozav replaying a session it already issued. The
// URL alone is useless — the customer create needs the id — so this has to be
// an error, and a retryable one: the next attempt mints a fresh session.
func TestCreateTosSessionRefusesAReplayThatCarriesNoToken(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"object": "terms_session", "status": "pending",
			"terms_url": "https://ozav.io/terms", "terms_version": "2.0",
			"expires_at": "2026-09-03T18:00:00Z"
		}`))
	})

	_, err := adapter.CreateTosSession(context.Background(), ramp.NewCustomerRef(7, nil), ramp.TosOpts{})

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

// accepted_at is omitted on purpose: Ozav defaults it to the time of the call,
// which is within a second of the user's tap, and a timestamp we compute is one
// that can arrive skewed or in the future — the documented refusal.
func TestAcceptTosSendsTheEvidenceAndNoTimestamp(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"object":"terms_session","status":"accepted",
			"terms_url":"https://ozav.io/terms","terms_version":"2.0",
			"expires_at":"2026-09-03T18:00:00Z","accepted_at":"2026-09-02T17:41:09Z"}`))
	})

	err := adapter.AcceptTos(context.Background(), ramp.NewCustomerRef(7, nil), ramp.TosAttestation{
		SessionID: "ts_abc", LegalName: "Marina Alves Ribeiro", Email: "marina@example.com",
		IPAddress: "203.0.113.42", UserAgent: "xip-android/1.0.3",
	})

	require.NoError(t, err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers/terms-sessions/accept", got.path)
	require.Equal(t, "ts_abc", got.body["terms_session_id"])
	require.Equal(t, map[string]any{"legal_name": "Marina Alves Ribeiro", "email": "marina@example.com"},
		got.body["accepted_by"])
	require.Equal(t, "203.0.113.42", got.body["ip_address"])
	require.Equal(t, "xip-android/1.0.3", got.body["user_agent"])
	require.NotContains(t, got.body, "accepted_at")
}

// Evidence Ozav does not require must not be invented, and an empty string is
// an invention: it says "we looked and there was none" where the truth is that
// the request carried no such header.
func TestAcceptTosOmitsEvidenceItDoesNotHave(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"object":"terms_session","status":"accepted",
			"terms_url":"https://ozav.io/terms","terms_version":"2.0",
			"expires_at":"2026-09-03T18:00:00Z"}`))
	})

	err := adapter.AcceptTos(context.Background(), ramp.NewCustomerRef(7, nil),
		ramp.TosAttestation{SessionID: "ts_abc", LegalName: "Marina Alves Ribeiro"})

	require.NoError(t, err)
	require.NotContains(t, got.body, "ip_address")
	require.NotContains(t, got.body, "user_agent")
	require.Equal(t, map[string]any{"legal_name": "Marina Alves Ribeiro"}, got.body["accepted_by"])
}

// A 200 whose status is not `accepted` would have us record a signature the
// provider does not think it has, and the customer create would then be refused
// with terms the row says are signed — the one state nothing recovers from.
func TestAcceptTosRefusesAResponseThatIsNotAccepted(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"object":"terms_session","status":"pending",
			"terms_url":"https://ozav.io/terms","terms_version":"2.0",
			"expires_at":"2026-09-03T18:00:00Z"}`))
	})

	err := adapter.AcceptTos(context.Background(), ramp.NewCustomerRef(7, nil),
		ramp.TosAttestation{SessionID: "ts_abc", LegalName: "Marina Alves Ribeiro"})

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

// Both refusals mean the same thing to the flow — the session this side holds
// is dead — and neither may read as ErrTosRequired, which would send the client
// back to the very session that just failed.
//
// The 404 in particular has to be matched on the code: the path fallback maps
// any 404 under /customers to ErrIntegrationRequired, which would tell the user
// to link an account they already have.
func TestAcceptTosNamesTheDeadSessionRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"expired", http.StatusUnprocessableEntity,
			`{"error":{"type":"invalid_request_error","code":"TERMS_SESSION_EXPIRED","message":"expired"}}`,
			ramp.ErrTosSessionExpired},
		{"unknown", http.StatusNotFound,
			`{"error":{"type":"invalid_request_error","code":"TERMS_SESSION_NOT_FOUND","message":"no such session"}}`,
			ramp.ErrTosSessionUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			err := adapter.AcceptTos(context.Background(), ramp.NewCustomerRef(7, nil),
				ramp.TosAttestation{SessionID: "ts_abc", LegalName: "Marina Alves Ribeiro"})

			require.ErrorIs(t, err, tc.want)
		})
	}
}

// The accepted session's token has to reach the customer create, or Ozav
// refuses it with the very error this whole flow exists to clear. It travels
// on the application as SignedAgreementID — the field that means, across every
// provider, "the proof of accepted terms this provider issued".
func TestSubmitApplicationNamesTheTermsSessionOnCreate(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"approved"}`))
	})

	app := fullApplication()
	app.SignedAgreementID = "ts_8Kd2mQ4vR7xN1pL5wT3yB6zA9cF0hJ2s"
	_, err := adapter.SubmitApplication(context.Background(), ramp.NewCustomerRef(7, nil), app)

	require.NoError(t, err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "ts_8Kd2mQ4vR7xN1pL5wT3yB6zA9cF0hJ2s", got.body["terms_session_id"])
}

// PATCH refuses the key outright — measured 02/09/2026:
// 400 INVALID_REQUEST, "Unrecognized key(s) in object: 'terms_session_id'",
// against the identical body that answers 200 without it. Since a resubmission
// after a rejection is a PATCH, sending it on both verbs would break every
// retry a rejected user makes, which is the same way `type` broke them before.
func TestSubmitApplicationOmitsTheTermsSessionOnUpdate(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"approved"}`))
	})

	app := fullApplication()
	app.SignedAgreementID = "ts_8Kd2mQ4vR7xN1pL5wT3yB6zA9cF0hJ2s"
	external := "ozav_cus_1"
	_, err := adapter.SubmitApplication(context.Background(), ramp.NewCustomerRef(7, &external), app)

	require.NoError(t, err)
	require.Equal(t, http.MethodPatch, got.method)
	require.NotContains(t, got.body, "terms_session_id")
}

// The customer object does not echo the terms session, so reporting any status
// here is reporting something Ozav did not say. It used to report
// not_required, which was true while Ozav had no terms — and would now
// overwrite the row's `approved` on every read, sending a user who has already
// accepted back through the terms step and, worse, leaving the gate to be
// satisfied by an id whose acceptance the row no longer records.
//
// Empty means "no opinion", and the service leaves the stored value alone —
// the same posture Bridge takes for the agreement it does not echo either.
func TestCustomerStateReportsNoOpinionOnTerms(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"approved"}`))
	})

	external := "ozav_cus_1"
	state, err := adapter.GetKyc(context.Background(), ramp.NewCustomerRef(7, &external))

	require.NoError(t, err)
	require.Empty(t, state.TosStatus)
}
