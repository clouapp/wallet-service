package sweep

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/macrowallets/waas/tests/mocks"
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestConsolidateAll_ShortPassphraseRejected verifies the input guard fires
// before any repository / lock / quota work, so a bad call cannot burn the
// per-account daily quota.
func TestConsolidateAll_SweepFlagStopsBeforeWalletLookup(t *testing.T) {
	paused := errors.New("sweep_paused")
	svc := &service{flags: func(context.Context, uuid.UUID) error { return paused }}

	_, err := svc.ConsolidateAll(context.Background(), uuid.New(), "eth", "passphrase12345", uuid.New())
	if !errors.Is(err, paused) {
		t.Fatalf("got %v", err)
	}
}

func TestConsolidateAll_ShortPassphraseRejected(t *testing.T) {
	svc := &service{}
	_, err := svc.ConsolidateAll(context.Background(), uuid.New(), "usdt", "short", uuid.Nil)
	if err == nil {
		t.Fatal("expected error for short passphrase, got nil")
	}
	if err.Error() != "passphrase must be at least 12 characters" {
		t.Fatalf("expected passphrase-length error, got %v", err)
	}
}

func TestConsolidateAll_BitcoinNoChildren(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainBTC, DepositAddress: &baseAddr}
	chainEntity := &models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin}

	mockChain := mocks.NewMockChain(models.ChainBTC)
	mockChain.NativeAssetVal = models.NativeBTC
	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr}},
		chainRepo:   &fakeChainRepo{chain: chainEntity},
	}

	res, err := svc.ConsolidateAll(context.Background(), walletID, models.NativeBTC, "passphrase12345", uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || len(res.Sweeps) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestConsolidateAll_UnknownAdapter(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainETH, DepositAddress: &baseAddr}
	chainEntity := &models.Chain{ID: models.ChainETH, AdapterType: ""}

	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain(models.ChainETH))
	svc := &service{
		registry:   registry,
		walletRepo: &fakeWalletRepo{wallet: wallet},
		chainRepo:  &fakeChainRepo{chain: chainEntity},
	}

	_, err := svc.ConsolidateAll(context.Background(), walletID, models.NativeETH, "passphrase12345", uuid.Nil)
	if err != ErrUnsupportedChain {
		t.Fatalf("expected ErrUnsupportedChain, got %v", err)
	}
}

// TestConsolidateAll_NoEligibleChildren_Noop exercises the early-return path:
// the wallet only has its base address, so there is nothing to sweep and the
// function returns an empty Result without touching the MPC / signing
// pipeline. This also proves the call is safe to poll — back-to-back noops
// are cheap and allocate no rows.
func TestConsolidateAll_NoEligibleChildren_Noop(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
	}
	chainEntity := &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}

	mockChain := mocks.NewMockChain("eth")
	mockChain.NativeAssetVal = "eth"
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Address: addr, Asset: "eth", Amount: big.NewInt(0)}, nil
	}

	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)

	txRepo := &fakeTxRepo{}
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr}},
		chainRepo:   &fakeChainRepo{chain: chainEntity},
		txRepo:      txRepo,
	}

	res, err := svc.ConsolidateAll(context.Background(), walletID, "eth", "passphrase12345", uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil Result on noop")
	}
	if len(res.Sweeps) != 0 {
		t.Fatalf("expected 0 sweeps on noop, got %d", len(res.Sweeps))
	}
	if res.FailedStep != nil {
		t.Fatalf("expected nil FailedStep, got %+v", res.FailedStep)
	}
	if res.FinalWithdrawTx != nil {
		t.Fatalf("expected no final withdrawal tx for consolidate, got %+v", res.FinalWithdrawTx)
	}
	if len(txRepo.created) != 0 {
		t.Fatalf("expected 0 persisted txs on noop, got %d", len(txRepo.created))
	}
}

// TestConsolidateAll_QuotaNotBurnedOnInvalidPassphrase proves the fix for I3's
// Part B: an invalid passphrase must NOT consume the caller's daily quota.
// The order of operations in ConsolidateAll is now
//
//	load → chain guard → lock → LoadLimits → decryptShareA → incrDailyQuota
//
// so any failure in decryptShareA short-circuits before the counter moves.
// The wallet here has a garbage ciphertext so any passphrase yields
// ErrInvalidPassphrase from mpcpkg.DecryptShare; after the failed call, the
// Redis quota key for the caller must be absent (count 0).
func TestConsolidateAll_QuotaNotBurnedOnInvalidPassphrase(t *testing.T) {
	client := testutil.TestRedis(t)

	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
		// Intentionally garbage ciphertext/iv/salt — hex-decodes OK so we
		// reach mpcpkg.DecryptShare, which fails AES-GCM auth and returns
		// ErrInvalidPassphrase regardless of the passphrase supplied.
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabecafebabecafebabe", // 12 bytes hex-decoded (valid GCM nonce length)
		MPCShareSalt:     "feedfacefeedfacefeedfacefeedface",
	}
	chainEntity := &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}

	mockChain := mocks.NewMockChain("eth")
	mockChain.NativeAssetVal = "eth"
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		// One eligible child so the flow progresses to the decrypt step
		// instead of bailing on the empty-eligible early return.
		amount := big.NewInt(0)
		if addr == "CHILD_A" {
			amount = big.NewInt(1000)
		}
		return &types.Balance{Address: addr, Asset: "eth", Amount: amount}, nil
	}

	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)

	callerAccountID := uuid.New()
	quotaKey := fmt.Sprintf(
		"vault:quota:consolidate:%s:%s",
		callerAccountID.String(),
		time.Now().UTC().Format("2006-01-02"),
	)
	lockKey := "vault:lock:wallet_ops:" + walletID.String()
	t.Cleanup(func() {
		ctx := context.Background()
		_ = client.Del(ctx, quotaKey).Err()
		_ = client.Del(ctx, lockKey).Err()
	})

	svc := &service{
		registry:    registry,
		rdb:         redisStore{client: client},
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr, childA}},
		chainRepo:   &fakeChainRepo{chain: chainEntity},
		txRepo:      &fakeTxRepo{},
	}

	_, err := svc.ConsolidateAll(context.Background(), walletID, "eth", "wrongpassphrase123", callerAccountID)
	if err == nil {
		t.Fatal("expected invalid-passphrase error, got nil")
	}
	if err.Error() != "invalid passphrase" {
		t.Fatalf("expected \"invalid passphrase\", got %q", err.Error())
	}

	count, getErr := client.Get(context.Background(), quotaKey).Int()
	if getErr != nil && !errors.Is(getErr, redis.Nil) {
		t.Fatalf("reading quota counter: %v", getErr)
	}
	if getErr == nil && count != 0 {
		t.Fatalf("expected quota counter to remain 0 after invalid passphrase, got %d", count)
	}
}
