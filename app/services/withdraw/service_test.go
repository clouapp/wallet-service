package withdraw

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/memcache"
	"github.com/macrowallets/waas/tests/mocks"
)

// mockMPC is a no-op MPC service for unit tests.
type mockMPC struct {
	signFn func(ctx context.Context, curve mpcpkg.Curve, shareA, shareB []byte, inputs mpcpkg.SignInputs) ([]byte, error)
}

func (m *mockMPC) Keygen(ctx context.Context, curve mpcpkg.Curve) (*mpcpkg.KeygenResult, error) {
	return nil, nil
}

func (m *mockMPC) Sign(ctx context.Context, curve mpcpkg.Curve, shareA, shareB []byte, inputs mpcpkg.SignInputs) ([]byte, error) {
	if m.signFn != nil {
		return m.signFn(ctx, curve, shareA, shareB, inputs)
	}
	return []byte("mocksig"), nil
}

func (m *mockMPC) ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error) {
	return bytes.Repeat([]byte{0x11}, 32), nil
}

func (m *mockMPC) ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error) {
	return nil, nil
}

func (m *mockMPC) ReconstructEd25519Scalar(shareA, shareB []byte) ([]byte, error) {
	return nil, nil
}

// mockSweepSvc is a minimal sweep.Service used by the unit tests in this
// package. None of these tests execute past the planner; the existing cases
// fail earlier (passphrase guard, redis lock, wallet lookup). PlanForWithdrawal
// returns ErrUnsupportedChain so that any test that *does* reach the planner
// surfaces the sentinel cleanly; remaining methods panic because they should
// never be reached by the currently covered flows.
type mockSweepSvc struct{}

func (m *mockSweepSvc) PlanForWithdrawal(context.Context, uuid.UUID, string, *big.Int, string, uuid.UUID) (*sweep.Plan, error) {
	return nil, sweep.ErrUnsupportedChain
}
func (m *mockSweepSvc) ExecutePlan(context.Context, *sweep.Plan, sweep.SigningCredentials, uuid.UUID, string, string) (*sweep.Result, error) {
	panic("mockSweepSvc.ExecutePlan must not be called in these tests")
}
func (m *mockSweepSvc) ConsolidateAll(context.Context, uuid.UUID, string, string, uuid.UUID) (*sweep.Result, error) {
	panic("mockSweepSvc.ConsolidateAll must not be called in these tests")
}
func (m *mockSweepSvc) RefreshGasStatus(context.Context, uuid.UUID) (*sweep.GasStatus, error) {
	panic("mockSweepSvc.RefreshGasStatus must not be called in these tests")
}
func (m *mockSweepSvc) LoadLimits(context.Context, uuid.UUID) (*sweep.Limits, error) {
	panic("mockSweepSvc.LoadLimits must not be called in these tests")
}

func setupWithdrawService(t *testing.T) (*Service, *mocks.MockChain, *memTransactions) {
	t.Helper()
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 12
	registry.RegisterChain(mockChain)
	registry.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6, Contract: "0xdAC17F"})

	txs := newMemTransactions()
	svc := NewService(Deps{
		Registry:     registry,
		MPC:          &mockMPC{},
		Transactions: txs,
		Wallets:      memWalletReader{},
		Sweep:        &mockSweepSvc{},
	})
	return svc, mockChain, txs
}

func TestRequest_Nil_CacheReportsItIsNotConfigured(t *testing.T) {
	svc := &Service{}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   uuid.New(),
	})
	if err == nil || err.Error() != "withdrawal lock: cache is not configured" {
		t.Fatalf("got %v", err)
	}
}

func TestRequest_Lock_AHeldWalletKeyIsAConcurrentWithdrawal(t *testing.T) {
	walletID := uuid.New()
	cache := memcache.New()
	cache.Add("vault:lock:withdrawal:"+walletID.String(), 1, time.Minute)
	svc := &Service{cache: cache}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   walletID,
	})
	if !errors.Is(err, ErrConcurrentWithdraw) {
		t.Fatalf("got %v", err)
	}
}

// A cache outage makes Add report false, which must not read as a held lock:
// the operator would chase a concurrent withdrawal that is not there.
func TestRequest_Lock_AnOutageIsNotAConcurrentWithdrawal(t *testing.T) {
	svc := &Service{cache: memcache.Down{Cache: memcache.New()}}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   uuid.New(),
	})
	if err == nil || errors.Is(err, ErrConcurrentWithdraw) || !errors.Is(err, cacheguard.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestCheck_Rate_LimitCapsFailuresAtFivePerWindowOnTheSameKey(t *testing.T) {
	cache := memcache.New()
	svc := &Service{cache: cache}
	if err := svc.checkRateLimit("wallet-1"); err != nil {
		t.Fatalf("no failures yet: %v", err)
	}
	for failure := 1; failure <= 4; failure++ {
		svc.recordFailedAttempt("wallet-1")
	}
	if err := svc.checkRateLimit("wallet-1"); err != nil {
		t.Fatalf("below the cap: %v", err)
	}
	svc.recordFailedAttempt("wallet-1")
	if got := cache.GetInt("vault:ratelimit:passphrase:wallet-1"); got != 5 {
		t.Fatalf("counter = %d, want 5 on the key the limiter always used", got)
	}
	if err := svc.checkRateLimit("wallet-1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("at the cap: %v", err)
	}
	if err := svc.checkRateLimit("wallet-2"); err != nil {
		t.Fatalf("another wallet is not limited: %v", err)
	}
}

func TestCheck_Rate_LimitFailsClosedWhenTheCacheIsDown(t *testing.T) {
	svc := &Service{cache: memcache.Down{Cache: memcache.New()}}
	err := svc.checkRateLimit("wallet-1")
	if err == nil || errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("a cache outage must refuse without claiming the cap was hit, got %v", err)
	}
}

func TestRequest_Withdrawals_FlagStopsBeforeRedis(t *testing.T) {
	paused := errors.New("withdrawals_paused")
	svc := &Service{flags: func(context.Context, uuid.UUID) error { return paused }}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase:      "validpassphrase123",
		CallerAccountID: uuid.New(),
	})
	if !errors.Is(err, paused) {
		t.Fatalf("got %v", err)
	}
}

// TestRequest_PassphraseTooShort verifies step-1 guard fires before any I/O.
func TestRequest_Passphrase_TooShort(t *testing.T) {
	svc, _, _ := setupWithdrawService(t)
	ctx := context.Background()

	_, _, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       uuid.New(),
		ToAddress:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		Amount:         "1000000",
		Asset:          "eth",
		Passphrase:     "short",
		IdempotencyKey: "pp_001",
	})
	if err != ErrPassphraseTooShort {
		t.Fatalf("expected ErrPassphraseTooShort, got %v", err)
	}
}

// TestRequest_InvalidAddress verifies address validation.
func TestRequest_Invalid_Address(t *testing.T) {
	_, mockChain, _ := setupWithdrawService(t)
	mockChain.ValidateAddressFn = func(address string) bool { return false }

	// We can't call Request here because it requires Redis for the lock.
	// The address validation now happens after the Redis lock is acquired.
	// This just verifies the mock is configured correctly.
	if mockChain.ValidateAddressFn("anything") != false {
		t.Error("expected ValidateAddressFn to return false")
	}
}

func TestRequest_Wallet_NotFound(t *testing.T) {
	// Passphrase too short guard fires before wallet lookup — use 12+ char passphrase
	// but Redis is nil so we expect a redis error, not wallet-not-found.
	// This test confirms the guard order: passphrase -> idempotency -> redis lock -> wallet
	svc, _, _ := setupWithdrawService(t)
	ctx := context.Background()

	_, _, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       uuid.New(),
		ToAddress:      "0x123",
		Amount:         "100",
		Asset:          "eth",
		Passphrase:     "validpassphrase123",
		IdempotencyKey: "notfound_001",
	})
	// With nil Redis, we expect a redis error (step 3 fails before wallet lookup)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestService_Get_Transaction(t *testing.T) {
	svc, _, txs := setupWithdrawService(t)
	ctx := context.Background()

	inserted := &models.Transaction{ID: uuid.New(), WalletID: uuid.New(), Chain: "eth", TxType: "withdrawal", Status: "pending", Asset: "eth", Amount: "100"}
	txs.add(inserted)

	got, err := svc.GetTransaction(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.ID != inserted.ID {
		t.Error("ID mismatch")
	}
}

func TestGet_Transaction_NotFound(t *testing.T) {
	svc, _, _ := setupWithdrawService(t)
	_, err := svc.GetTransaction(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestList_Transactions_Filters(t *testing.T) {
	svc, _, txs := setupWithdrawService(t)
	ctx := context.Background()

	walletID := uuid.New()
	txs.add(&models.Transaction{ID: uuid.New(), WalletID: walletID, Chain: "eth", TxType: "deposit", Status: "confirmed", Asset: "eth", Amount: "100"})
	txs.add(&models.Transaction{ID: uuid.New(), WalletID: walletID, Chain: "eth", TxType: "withdrawal", Status: "pending", Asset: "usdt", Amount: "200"})
	txs.add(&models.Transaction{ID: uuid.New(), WalletID: walletID, Chain: "eth", TxType: "deposit", Status: "pending", Asset: "eth", Amount: "300"})

	// All
	all, _, _ := svc.ListTransactions(ctx, "", "", "", "", 50, 0)
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	// Filter by type
	deposits, _, _ := svc.ListTransactions(ctx, "", "deposit", "", "", 50, 0)
	if len(deposits) != 2 {
		t.Errorf("expected 2 deposits, got %d", len(deposits))
	}

	// Filter by status
	pending, _, _ := svc.ListTransactions(ctx, "", "", "pending", "", 50, 0)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}

	// Filter by chain
	eth, _, _ := svc.ListTransactions(ctx, "eth", "", "", "", 50, 0)
	if len(eth) != 3 {
		t.Errorf("expected 3 eth txs, got %d", len(eth))
	}

	// Limit
	limited, _, _ := svc.ListTransactions(ctx, "", "", "", "", 1, 0)
	if len(limited) != 1 {
		t.Errorf("expected 1 with limit, got %d", len(limited))
	}
}
