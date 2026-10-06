package jobs

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestRefreshWalletBalancesRejectsEmptyArgs(t *testing.T) {
	j := &RefreshWalletBalances{}
	if j.Signature() != "refresh_wallet_balances" {
		t.Fatalf("unexpected signature: %s", j.Signature())
	}
	err := j.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRefreshWalletBalancesRejectsInvalidUUID(t *testing.T) {
	j := &RefreshWalletBalances{}
	err := j.Handle(mustWalletArg("not-a-uuid", "eth"))
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestRefreshWalletTransactionsRejectsEmptyArgs(t *testing.T) {
	j := &RefreshWalletTransactions{}
	err := j.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRefreshWalletTokensRejectsEmptyArgs(t *testing.T) {
	j := &RefreshWalletTokens{}
	err := j.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRefreshWalletUTXOsRejectsEmptyArgs(t *testing.T) {
	j := &RefreshWalletUTXOs{}
	err := j.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestReconcileWalletStateRejectsEmptyArgs(t *testing.T) {
	j := &ReconcileWalletState{}
	err := j.Handle()
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRefreshWalletBalancesRetryPolicy(t *testing.T) {
	j := &RefreshWalletBalances{}
	retry, delay := j.ShouldRetry(retryableServerError(), 1)
	if !retry {
		t.Fatal("expected retry on attempt 1")
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}

	retry, _ = j.ShouldRetry(retryableServerError(), 5)
	if retry {
		t.Fatal("expected no retry on attempt 5")
	}
}

func TestRefreshWalletTransactionsRetryPolicy(t *testing.T) {
	j := &RefreshWalletTransactions{}
	retry, delay := j.ShouldRetry(retryableServerError(), 1)
	if !retry {
		t.Fatal("expected retry on attempt 1")
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	retry, _ = j.ShouldRetry(retryableServerError(), 5)
	if retry {
		t.Fatal("expected no retry at attempt 5")
	}
}

func TestRefreshWalletTokensRetryPolicy(t *testing.T) {
	j := &RefreshWalletTokens{}
	retry, delay := j.ShouldRetry(retryableServerError(), 1)
	if !retry {
		t.Fatal("expected retry on attempt 1")
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	retry, _ = j.ShouldRetry(retryableServerError(), 5)
	if retry {
		t.Fatal("expected no retry at attempt 5")
	}
}

func TestRefreshWalletUTXOsRetryPolicy(t *testing.T) {
	j := &RefreshWalletUTXOs{}
	retry, delay := j.ShouldRetry(retryableServerError(), 1)
	if !retry {
		t.Fatal("expected retry on attempt 1")
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	retry, _ = j.ShouldRetry(retryableServerError(), 5)
	if retry {
		t.Fatal("expected no retry at attempt 5")
	}
}

func TestReconcileWalletStateRetryPolicy(t *testing.T) {
	j := &ReconcileWalletState{}
	retry, delay := j.ShouldRetry(retryableServerError(), 1)
	if !retry {
		t.Fatal("expected retry on attempt 1")
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	retry, _ = j.ShouldRetry(retryableServerError(), 5)
	if retry {
		t.Fatal("expected no retry at attempt 5")
	}
}

func TestRetryDelayScalesWithAttempt(t *testing.T) {
	j := &RefreshWalletBalances{}
	for attempt := 1; attempt < 5; attempt++ {
		retry, delay := j.ShouldRetry(retryableServerError(), attempt)
		if !retry {
			t.Fatalf("expected retry on attempt %d", attempt)
		}
		expected := time.Duration(attempt) * 5 * time.Second
		if delay != expected {
			t.Fatalf("attempt %d: expected %v, got %v", attempt, expected, delay)
		}
	}
}

func TestShouldRetryDistinguishesKnownFailureFromUnknownOutcome(t *testing.T) {
	j := &RefreshWalletBalances{}
	known := []error{
		retryableServerError(),
		chain.FromProviderHTTP(http.StatusInternalServerError, "down"),
		chain.FromProviderHTTP(http.StatusServiceUnavailable, "down"),
		chain.FromProviderHTTP(http.StatusGatewayTimeout, "down"),
		fmtRateLimit(),
		timeoutBeforeSend(t),
	}
	for _, err := range known {
		retry, delay := j.ShouldRetry(err, 1)
		if !retry || delay != 5*time.Second {
			t.Fatalf("known failure %v: retry=%v delay=%v", err, retry, delay)
		}
	}

	unknown := []error{
		errors.New("test"),
		chain.ErrUnknownOutcome,
		chain.UnknownOutcome(errors.New("broadcast result unknown")),
		chain.UnknownOutcome(chain.FromProviderHTTP(http.StatusBadGateway, "maybe accepted")),
		chain.FromProviderHTTP(http.StatusBadRequest, "rejected"),
		chain.FromProviderHTTP(http.StatusNotFound, "missing"),
	}
	for _, err := range unknown {
		if retry, delay := j.ShouldRetry(err, 1); retry || delay != 0 {
			t.Fatalf("unknown outcome %v: retry=%v delay=%v", err, retry, delay)
		}
	}
}

func retryableServerError() error {
	return chain.FromProviderHTTP(http.StatusBadGateway, "down")
}

func fmtRateLimit() error {
	return errors.Join(errors.New("rpc call"), chain.ErrRateLimited)
}

func timeoutBeforeSend(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := httpclient.NewClient(time.Second).Do(ctx, httpclient.Request{
		Method: httpclient.MethodGet,
		URL:    "http://127.0.0.1:1",
	})
	if !httpclient.IsBuild(err) || !chain.KnownFailure(err) {
		t.Fatalf("timeout before send: %v", err)
	}
	return err
}

func TestRefreshWalletBalancesRejectsEmptyChainID(t *testing.T) {
	j := &RefreshWalletBalances{}
	err := j.Handle(mustWalletArg("00000000-0000-0000-0000-000000000001", ""))
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestRefreshWalletTransactionsRejectsInvalidUUID(t *testing.T) {
	j := &RefreshWalletTransactions{}
	err := j.Handle(mustWalletArg("not-a-uuid", "eth"))
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestRefreshWalletTransactionsRejectsEmptyChainID(t *testing.T) {
	j := &RefreshWalletTransactions{}
	err := j.Handle(mustWalletArg("00000000-0000-0000-0000-000000000001", ""))
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestRefreshWalletTokensRejectsInvalidUUID(t *testing.T) {
	j := &RefreshWalletTokens{}
	err := j.Handle(mustWalletArg("not-a-uuid", "eth"))
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestRefreshWalletTokensRejectsEmptyChainID(t *testing.T) {
	j := &RefreshWalletTokens{}
	err := j.Handle(mustWalletArg("00000000-0000-0000-0000-000000000001", ""))
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestRefreshWalletUTXOsRejectsInvalidUUID(t *testing.T) {
	j := &RefreshWalletUTXOs{}
	err := j.Handle(mustWalletArg("not-a-uuid", "btc"))
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestRefreshWalletUTXOsRejectsEmptyChainID(t *testing.T) {
	j := &RefreshWalletUTXOs{}
	err := j.Handle(mustWalletArg("00000000-0000-0000-0000-000000000001", ""))
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestReconcileWalletStateRejectsInvalidUUID(t *testing.T) {
	j := &ReconcileWalletState{}
	err := j.Handle(mustWalletArg("not-a-uuid", "eth"))
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestReconcileWalletStateRejectsEmptyChainID(t *testing.T) {
	j := &ReconcileWalletState{}
	err := j.Handle(mustWalletArg("00000000-0000-0000-0000-000000000001", ""))
	if err == nil {
		t.Fatal("expected error for empty chain_id")
	}
}

func TestAllJobSignatures(t *testing.T) {
	expected := map[string]string{
		"RefreshWalletBalances":     "refresh_wallet_balances",
		"RefreshWalletTransactions": "refresh_wallet_transactions",
		"RefreshWalletTokens":       "refresh_wallet_tokens",
		"RefreshWalletUTXOs":        "refresh_wallet_utxos",
		"ReconcileWalletState":      "reconcile_wallet_state",
	}
	jobs := []struct {
		name string
		sig  string
	}{
		{"RefreshWalletBalances", (&RefreshWalletBalances{}).Signature()},
		{"RefreshWalletTransactions", (&RefreshWalletTransactions{}).Signature()},
		{"RefreshWalletTokens", (&RefreshWalletTokens{}).Signature()},
		{"RefreshWalletUTXOs", (&RefreshWalletUTXOs{}).Signature()},
		{"ReconcileWalletState", (&ReconcileWalletState{}).Signature()},
	}
	for _, j := range jobs {
		exp, ok := expected[j.name]
		if !ok {
			t.Errorf("no expected signature for %s", j.name)
			continue
		}
		if exp != j.sig {
			t.Errorf("%s: expected %q, got %q", j.name, exp, j.sig)
		}
	}
}

func TestAllJobsRejectNonStringWalletID(t *testing.T) {
	tests := []struct {
		name string
		job  interface{ Handle(args ...any) error }
	}{
		{"RefreshWalletBalances", &RefreshWalletBalances{}},
		{"RefreshWalletTransactions", &RefreshWalletTransactions{}},
		{"RefreshWalletTokens", &RefreshWalletTokens{}},
		{"RefreshWalletUTXOs", &RefreshWalletUTXOs{}},
		{"ReconcileWalletState", &ReconcileWalletState{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.job.Handle(`{"wallet_id":123,"chain_id":"eth"}`)
			if err == nil {
				t.Fatal("expected error for non-string wallet_id")
			}
		})
	}
}

func TestWalletJobsCallOneServiceMethod(t *testing.T) {
	rec := &recordingWalletQueue{}
	id := "11111111-1111-1111-1111-111111111111"
	jobs := []struct {
		name string
		job  interface{ Handle(args ...any) error }
		got  *int
	}{
		{"balances", &RefreshWalletBalances{wallets: rec}, &rec.balances},
		{"transactions", &RefreshWalletTransactions{wallets: rec}, &rec.transactions},
		{"tokens", &RefreshWalletTokens{wallets: rec}, &rec.tokens},
		{"utxos", &RefreshWalletUTXOs{wallets: rec}, &rec.utxos},
		{"reconcile", &ReconcileWalletState{wallets: rec}, &rec.reconcile},
	}
	for _, tc := range jobs {
		t.Run(tc.name, func(t *testing.T) {
			before := *tc.got
			if err := tc.job.Handle(mustWalletArg(id, "eth")); err != nil {
				t.Fatal(err)
			}
			if *tc.got != before+1 {
				t.Fatalf("service calls = %d, want %d", *tc.got, before+1)
			}
		})
	}
	if rec.balances != 1 || rec.transactions != 1 || rec.tokens != 1 || rec.utxos != 1 || rec.reconcile != 1 {
		t.Fatalf("calls = %+v, each job must call only its own method", rec)
	}
}

type recordingWalletQueue struct {
	balances, transactions, tokens, utxos, reconcile int
}

func (r *recordingWalletQueue) RefreshBalances(context.Context, uuid.UUID, string) error {
	r.balances++
	return nil
}

func (r *recordingWalletQueue) RefreshTransactions(context.Context, uuid.UUID, string) error {
	r.transactions++
	return nil
}

func (r *recordingWalletQueue) RefreshTokens(context.Context, uuid.UUID, string) error {
	r.tokens++
	return nil
}

func (r *recordingWalletQueue) RefreshUTXOs(context.Context, uuid.UUID, string) error {
	r.utxos++
	return nil
}

func (r *recordingWalletQueue) ReconcileWallet(context.Context, uuid.UUID, string) error {
	r.reconcile++
	return nil
}

func TestWalletJobsRefusePositionalArgs(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	rec := &recordingWalletQueue{}
	job := &RefreshWalletBalances{wallets: rec}
	if err := job.Handle(id, "eth"); err == nil {
		t.Fatal("positional args must be refused")
	}
	if rec.balances != 0 {
		t.Fatal("positional args must not call the service")
	}
}

func mustWalletArg(walletID, chainID string) any {
	args, err := WalletArgs(walletID, chainID)
	if err != nil {
		panic(err)
	}
	return args[0].Value
}

func TestAllJobsRejectSingleArg(t *testing.T) {
	tests := []struct {
		name string
		job  interface{ Handle(args ...any) error }
	}{
		{"RefreshWalletBalances", &RefreshWalletBalances{}},
		{"RefreshWalletTransactions", &RefreshWalletTransactions{}},
		{"RefreshWalletTokens", &RefreshWalletTokens{}},
		{"RefreshWalletUTXOs", &RefreshWalletUTXOs{}},
		{"ReconcileWalletState", &ReconcileWalletState{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.job.Handle("some-wallet-id")
			if err == nil {
				t.Fatal("expected error for single arg")
			}
		})
	}
}
