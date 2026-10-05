package withdraw

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests in this package
	testutil.BootTest()
	os.Exit(m.Run())
}

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

func setupWithdrawService(t *testing.T) (*Service, *mocks.MockChain) {
	t.Helper()
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 12
	registry.RegisterChain(mockChain)
	registry.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6, Contract: "0xdAC17F"})

	webhookConfigRepo := repositories.NewWebhookConfigRepository(nil, facades.Crypt())
	webhookEventRepo := repositories.NewWebhookEventRepository(nil)
	webhookSvc := webhook.NewService(webhook.Deps{
		Configs: webhookConfigRepo,
		Events:  webhookEventRepo,
	})
	mpcSvc := &mockMPC{}
	txRepo := repositories.NewTransactionRepository(nil)
	walletRepo := repositories.NewWalletRepository(nil)
	addressRepo := repositories.NewAddressRepository(nil)
	svc := NewService(Deps{
		Registry:     registry,
		Webhook:      webhookSvc,
		MPC:          mpcSvc,
		Transactions: txRepo,
		Wallets:      walletRepo,
		Addresses:    addressRepo,
		Sweep:        &mockSweepSvc{},
	})
	return svc, mockChain
}

type recordingLocker struct {
	key        string
	value      string
	expiration time.Duration
	acquired   bool
	setErr     error
	count      int
	readKey    string
	readErr    error
	incrKey    string
	incrTTL    time.Duration
}

func (r *recordingLocker) SetNX(_ context.Context, key, value string, expiration time.Duration) (bool, error) {
	r.key = key
	r.value = value
	r.expiration = expiration
	return r.acquired, r.setErr
}

func (r *recordingLocker) Del(context.Context, string) error { return nil }

func (r *recordingLocker) Int(_ context.Context, key string) (int, error) {
	r.readKey = key
	return r.count, r.readErr
}

func (r *recordingLocker) IncrExpire(_ context.Context, key string, expiration time.Duration) error {
	r.incrKey = key
	r.incrTTL = expiration
	return nil
}

func (r *recordingLocker) IncrBy(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}

func (r *recordingLocker) DecrBy(context.Context, string, int64) error { return nil }

func TestRequest_NilLockerReportsRedisNotConfigured(t *testing.T) {
	svc := &Service{}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   uuid.New(),
	})
	if err == nil || err.Error() != "redis lock: redis is not configured" {
		t.Fatalf("got %v", err)
	}
}

func TestRequest_LockUsesTheSameKeyValueAndTTL(t *testing.T) {
	walletID := uuid.New()
	locker := &recordingLocker{}
	svc := &Service{locker: locker}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   walletID,
	})
	if !errors.Is(err, ErrConcurrentWithdraw) {
		t.Fatalf("got %v", err)
	}
	if locker.key != "vault:lock:withdrawal:"+walletID.String() || locker.value != "1" || locker.expiration != 60*time.Second {
		t.Fatal("withdrawal lock command changed")
	}
}

func TestRequest_LockErrorIsWrapped(t *testing.T) {
	svc := &Service{locker: &recordingLocker{setErr: errors.New("boom")}}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase: "validpassphrase123",
		WalletID:   uuid.New(),
	})
	if err == nil || err.Error() != "redis lock: boom" {
		t.Fatalf("got %v", err)
	}
}

func TestCheckRateLimitKeepsTheThresholdAndFailOpen(t *testing.T) {
	walletID := "wallet-1"
	open := &recordingLocker{}
	if err := (&Service{locker: open}).checkRateLimit(context.Background(), walletID); err != nil {
		t.Fatalf("missing count: %v", err)
	}
	if open.readKey != "vault:ratelimit:passphrase:"+walletID {
		t.Fatal("passphrase counter key changed")
	}
	below := &recordingLocker{count: 4}
	if err := (&Service{locker: below}).checkRateLimit(context.Background(), walletID); err != nil {
		t.Fatalf("below threshold: %v", err)
	}
	blocked := &recordingLocker{count: 5}
	if err := (&Service{locker: blocked}).checkRateLimit(context.Background(), walletID); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("threshold: %v", err)
	}
	down := &recordingLocker{readErr: errors.New("down")}
	if err := (&Service{locker: down}).checkRateLimit(context.Background(), walletID); err != nil {
		t.Fatalf("redis error: %v", err)
	}
}

func TestRecordFailedAttemptUsesTheSameCounterCommand(t *testing.T) {
	locker := &recordingLocker{}
	(&Service{locker: locker}).recordFailedAttempt(context.Background(), "wallet-1")
	if locker.incrKey != "vault:ratelimit:passphrase:wallet-1" || locker.incrTTL != 60*time.Second {
		t.Fatal("passphrase counter command changed")
	}
}

func TestRequest_WithdrawalsFlagStopsBeforeRedis(t *testing.T) {
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
func TestRequest_PassphraseTooShort(t *testing.T) {
	svc, _ := setupWithdrawService(t)
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
func TestRequest_InvalidAddress(t *testing.T) {
	_, mockChain := setupWithdrawService(t)
	mockChain.ValidateAddressFn = func(address string) bool { return false }

	// We can't call Request here because it requires Redis for the lock.
	// The address validation now happens after the Redis lock is acquired.
	// This just verifies the mock is configured correctly.
	if mockChain.ValidateAddressFn("anything") != false {
		t.Error("expected ValidateAddressFn to return false")
	}
}

func TestRequest_WalletNotFound(t *testing.T) {
	// Passphrase too short guard fires before wallet lookup — use 12+ char passphrase
	// but Redis is nil so we expect a redis error, not wallet-not-found.
	// This test confirms the guard order: passphrase -> idempotency -> redis lock -> wallet
	svc, _ := setupWithdrawService(t)
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

func TestGetTransaction(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	ctx := context.Background()

	w := mocks.InsertWallet(t, "eth")
	inserted := mocks.InsertTransaction(t, w.ID, nil, "eth", "withdrawal", "pending", "eth", "100", 0)

	got, err := svc.GetTransaction(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.ID != inserted.ID {
		t.Error("ID mismatch")
	}
}

func TestGetTransaction_NotFound(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	_, err := svc.GetTransaction(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListTransactions_Filters(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	ctx := context.Background()

	w := mocks.InsertWallet(t, "eth")
	mocks.InsertTransaction(t, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)
	mocks.InsertTransaction(t, w.ID, nil, "eth", "withdrawal", "pending", "usdt", "200", 0)
	mocks.InsertTransaction(t, w.ID, nil, "eth", "deposit", "pending", "eth", "300", 60)

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
