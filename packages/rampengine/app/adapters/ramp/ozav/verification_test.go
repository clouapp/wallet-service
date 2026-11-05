package ozav

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// Opening a verification is what stops an Ozav customer from being verified.
//
// Measured on 03/09/2026 across every customer in the live organization —
// 23 of 23, with no exception:
//
//	20 customers, zero verifications  -> approved
//	 3 customers, one or two          -> pending, checks never leave not_started
//
// The 20 include accounts with identity documents uploaded and accounts with
// none, so documents are not what separates them. What separates them is
// whether anything ever called POST /verifications. The three stuck records
// appeared the day 23c1bbf shipped, which added that call to the submission
// path on the reasoning that Ozav's customer "stays there until something asks
// for the checks by name". The opposite is true.
//
// Ozav verifies an individual from the customer record and the documents
// uploaded against it. The flow is: create the customer with an accepted terms
// session, upload the documents, wait.
//
// These tests exist because the mistake is an attractive one. Every symptom it
// produces — a screening that never starts, still_required that keeps asking,
// a session_url pointing at a retired page — reads like a provider that has
// not been asked for enough, and the obvious repair is to ask it for more.

// recorder answers every request plausibly and remembers each path, so a test
// can assert on a call that was never made. The shared capture holds only the
// last request, which cannot express "and nothing else happened".
type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *recorder) saw(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seen := range r.paths {
		if seen == path {
			return true
		}
	}
	return false
}

func newRecordingAdapter(t *testing.T) (*Adapter, *recorder) {
	t.Helper()
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/customers" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"pending","verification":{"mode":"full"},
				"requirements":{"due":[],"missing_fields":[]}}`))
		default:
			_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"pending","verification":{"mode":"full"},
				"requirements":{"due":[],"missing_fields":[]},"data":[],"still_required":[]}`))
		}
	}))
	t.Cleanup(server.Close)

	cfg := Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second}
	client := NewClient(Config{APIKey: cfg.APIKey, Timeout: cfg.Timeout, BaseURL: server.URL})
	return &Adapter{clients: func(context.Context) (*Client, error) { return client, nil }}, rec
}

// The guard that matters: whatever the KYC surface is asked to do, no path
// through it opens a verification. Asserting on the whole surface rather than
// on one method is deliberate — the bug was introduced by adding a new caller,
// not by changing an existing one, and a per-method test would not have caught it.
func TestNoKycPathOpensAVerification(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	for _, act := range []struct {
		name string
		call func(*Adapter) error
	}{
		{"SubmitApplication", func(a *Adapter) error {
			_, err := a.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, fullApplication())
			return err
		}},
		{"GetKyc", func(a *Adapter) error {
			_, err := a.GetKyc(context.Background(), ref)
			return err
		}},
		{"RefreshKyc", func(a *Adapter) error {
			_, err := a.RefreshKyc(context.Background(), ref)
			return err
		}},
		{"GetRequirements", func(a *Adapter) error {
			_, err := a.GetRequirements(context.Background(), ref)
			return err
		}},
	} {
		t.Run(act.name, func(t *testing.T) {
			adapter, rec := newRecordingAdapter(t)

			_ = act.call(adapter)

			require.False(t, rec.saw("/verifications"),
				"%s opened a verification; on Ozav that freezes the customer at pending", act.name)
		})
	}
}

// The two remaining callers of POST /verifications refuse, and refuse locally.
//
// They are reachable from outside: RequestEndorsements is POST
// /api/kyc/{provider}/endorsements, and CreateHostedKycLink backs both the
// client's hosted-link route and the admin's button on a user's page. Leaving
// them callable would mean an operator could freeze a user's KYC from the admin
// panel, which is how this defect reached production the first time — through a
// path nobody thought of as "starting a verification".
func TestVerificationOpenersRefuseLocally(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	t.Run("RequestEndorsements", func(t *testing.T) {
		_, err := unreachableAdapter(t).RequestEndorsements(
			context.Background(), ref, []string{"pix"})

		require.ErrorIs(t, err, ramp.ErrVerificationFreezesCustomer)
	})

	t.Run("CreateHostedKycLink", func(t *testing.T) {
		_, err := unreachableAdapter(t).CreateHostedKycLink(
			context.Background(), ref, ramp.HostedLinkOpts{Endorsements: []string{"pix"}})

		require.ErrorIs(t, err, ramp.ErrVerificationFreezesCustomer)
	})
}

// A refusal that costs the caller its customer would be worse than the call.
// Neither method writes anything, so both are safe to refuse — but the refusal
// has to be the whole of what they do, with no partial work before it.
func TestRefusalLeavesNothingBehind(t *testing.T) {
	adapter, rec := newRecordingAdapter(t)
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	_, _ = adapter.RequestEndorsements(context.Background(), ref, []string{"pix"})
	_, _ = adapter.CreateHostedKycLink(context.Background(), ref, ramp.HostedLinkOpts{})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Empty(t, rec.paths, "a refused call must reach Ozav for nothing at all")
}

// Decoding is unaffected: the adapter still reads a customer that carries a
// verification block, because Ozav sends one whether or not we opened a
// session. Refusing to *open* one is not refusing to *read* one.
func TestCustomerVerificationBlockStillDecodes(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"approved",
			"verification":{"mode":"full","status":"approved","checks":{}},
			"requirements":{"due":[],"missing_fields":[]}}`))
	})

	state, err := adapter.GetKyc(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(state.Raw, &raw))
	require.Contains(t, raw, "verification")
}
