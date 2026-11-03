package ozav

import (
	"cmp"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// fakeOzav answers each "METHOD /path" with a canned reply and records every
// request, so a test can assert what was sent and what was not. A call to a
// route nobody set fails the test.
type fakeOzav struct {
	t        *testing.T
	mu       sync.Mutex
	routes   map[string]fakeReply
	requests []recordedRequest
}

type fakeReply struct {
	status int // zero answers 200
	body   string
}

func newFakeAdapter(t *testing.T, routes map[string]fakeReply) (*Adapter, *fakeOzav) {
	t.Helper()
	fake := &fakeOzav{t: t, routes: routes}
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(server.Close)
	client := NewClient(Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second, BaseURL: server.URL})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil), fake
}

func (f *fakeOzav) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{r.Method, r.URL.Path, r.Header.Clone(), body})
	f.mu.Unlock()

	reply, ok := f.routes[r.Method+" "+r.URL.Path]
	if !ok {
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(cmp.Or(reply.status, http.StatusOK))
	_, _ = w.Write([]byte(reply.body))
}

// call is the first request to method and path, or nil when none was made.
func (f *fakeOzav) call(method, path string) *recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.requests {
		if f.requests[i].method == method && f.requests[i].path == path {
			return &f.requests[i]
		}
	}
	return nil
}

// recordedRequest is one call the fake received, body included: an upload's
// body is raw bytes rather than JSON.
type recordedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

const oneManagedWallet = `{"data":[{"id":"ozav_wal_1","object":"wallet",
	"customer_id":"ozav_cus_1","custody_model":"managed","status":"active"}]}`

// onrampRoutes answer the two calls placing an on-ramp takes.
func onrampRoutes() map[string]fakeReply {
	return map[string]fakeReply{
		"GET /wallets":    {body: oneManagedWallet},
		"POST /transfers": {body: onrampTransferBody},
	}
}

const ozavDepositAddressBody = `{"data":[{"address":"OzavManagedDepositSol","network":"solana","currency":"USDC"}]}`

// offrampRoutes answer create (destination + deposit address) and settlement.
func offrampRoutes() map[string]fakeReply {
	return map[string]fakeReply{
		"GET /wallets": {body: oneManagedWallet},
		"GET /wallets/ozav_wal_1/deposit-addresses": {body: ozavDepositAddressBody},
		// Live Ozav's shape: amount, no available and no per-chain split.
		"GET /wallets/ozav_wal_1/balances": {body: `{"data":[{"currency":"USDC","amount":"1000"}]}`},
		"POST /payout-destinations":        {status: http.StatusCreated, body: payoutDestinationBody},
		"POST /transfers":                  {body: offrampTransferBody},
	}
}

// deliveryRoutes answer the two reads and the send a delivery takes.
func deliveryRoutes() map[string]fakeReply {
	return map[string]fakeReply{
		"GET /customers/ozav_cus_1": {body: deliveryCustomerBody},
		"GET /wallets":              {body: oneManagedWallet},
		"POST /transfers":           {body: deliveryTransferBody},
	}
}

// Every call about a customer refuses locally without one: the path cannot
// even be built, and a wasted upstream call on a doomed request is what the
// guard exists to prevent.
func TestCallsWithoutACustomerRefuseLocally(t *testing.T) {
	noCustomer := ramp.CustomerRef{CustomerID: 7}
	ctx := context.Background()
	for name, call := range map[string]func(*Adapter) error{
		"GetKyc":     func(a *Adapter) error { _, err := a.GetKyc(ctx, noCustomer); return err },
		"RefreshKyc": func(a *Adapter) error { _, err := a.RefreshKyc(ctx, noCustomer); return err },
		"GetRequirements": func(a *Adapter) error {
			_, err := a.GetRequirements(ctx, noCustomer)
			return err
		},
		"RefreshIntegration": func(a *Adapter) error {
			_, err := a.RefreshIntegration(ctx, noCustomer)
			return err
		},
		"SubmitDocuments": func(a *Adapter) error {
			_, err := a.SubmitDocuments(ctx, noCustomer, ramp.Documents{
				LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
			})
			return err
		},
		"EstimateOnramp": func(a *Adapter) error {
			_, err := a.EstimateOnramp(ctx, noCustomer, onrampQuote())
			return err
		},
		"EstimateOfframp": func(a *Adapter) error {
			_, err := a.EstimateOfframp(ctx, noCustomer, offrampQuote())
			return err
		},
		"CreateOnramp": func(a *Adapter) error { _, err := a.CreateOnramp(ctx, noCustomer, onrampCreate()); return err },
		"CreateOfframp": func(a *Adapter) error {
			_, err := a.CreateOfframp(ctx, noCustomer, offrampCreate())
			return err
		},
		"DeliverOnramp": func(a *Adapter) error {
			_, err := a.DeliverOnramp(ctx, noCustomer, deliveryRequest())
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, call(unreachableAdapter(t)), ramp.ErrIntegrationRequired)
		})
	}
}
