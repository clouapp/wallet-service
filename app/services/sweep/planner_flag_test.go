package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestPlanForWithdrawal_SweepEnabled(t *testing.T) {
	t.Parallel()

	accountOn := true
	accountOff := false
	globalOn := true
	globalOff := false

	cases := []struct {
		name    string
		global  *bool
		account *bool
		paused  bool
	}{
		{name: "missing row plans", paused: false},
		{name: "enabled true plans", account: &accountOn, paused: false},
		{name: "global true and account true plan", global: &globalOn, account: &accountOn, paused: false},
		{name: "account false pauses", account: &accountOff, paused: true},
		{name: "global true does not release account false", global: &globalOn, account: &accountOff, paused: true},
		{name: "global false pauses even when the account flag is true", global: &globalOff, account: &accountOn, paused: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			accountID := uuid.New()
			store := newPlanFlagStore()
			flags := features.NewService(features.Deps{Store: store, Admins: planFlagAdmins{}, Activity: planFlagActivity{}})
			ctx := context.Background()
			if tc.global != nil {
				if err := store.UpsertGlobal(ctx, features.FlagSweepEnabled, *tc.global); err != nil {
					t.Fatalf("global: %v", err)
				}
			}
			if tc.account != nil {
				if _, err := flags.Set(ctx, accountID, uuid.New(), "owner", features.FlagSweepEnabled, *tc.account); err != nil {
					t.Fatalf("account: %v", err)
				}
			}

			svc, walletRepo, txRepo, mockChain := planFlagService(t, accountID, flags)
			plan, err := svc.PlanForWithdrawal(ctx, walletRepo.wallet.ID, "eth", big.NewInt(500), "", accountID)

			if tc.paused {
				assertSweepPaused(t, err)
				if plan != nil {
					t.Fatal("paused plan must be nil")
				}
				if walletRepo.findCalls != 0 || txRepo.creates != 0 || mockChain.BroadcastTransactionCalls != 0 {
					t.Fatalf("paused work find=%d persist=%d broadcast=%d", walletRepo.findCalls, txRepo.creates, mockChain.BroadcastTransactionCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if plan == nil || plan.Strategy != StrategyDirectFromBase {
				t.Fatalf("plan = %+v", plan)
			}
			if txRepo.creates != 0 || mockChain.BroadcastTransactionCalls != 0 {
				t.Fatalf("planned persist=%d broadcast=%d", txRepo.creates, mockChain.BroadcastTransactionCalls)
			}
			if walletRepo.findCalls == 0 {
				t.Fatal("a live plan never read the wallet")
			}
		})
	}
}

func TestPlanForWithdrawal_WalletAccountOffPausesBeforePersist(t *testing.T) {
	t.Parallel()

	callerID := uuid.New()
	walletAccountID := uuid.New()
	store := newPlanFlagStore()
	flags := features.NewService(features.Deps{Store: store, Admins: planFlagAdmins{}, Activity: planFlagActivity{}})
	ctx := context.Background()
	if _, err := flags.Set(ctx, callerID, uuid.New(), "owner", features.FlagSweepEnabled, true); err != nil {
		t.Fatalf("caller on: %v", err)
	}
	if _, err := flags.Set(ctx, walletAccountID, uuid.New(), "owner", features.FlagSweepEnabled, false); err != nil {
		t.Fatalf("wallet account off: %v", err)
	}

	svc, walletRepo, txRepo, mockChain := planFlagService(t, walletAccountID, flags)
	plan, err := svc.PlanForWithdrawal(ctx, walletRepo.wallet.ID, "eth", big.NewInt(500), "", callerID)
	assertSweepPaused(t, err)
	if plan != nil {
		t.Fatal("paused plan must be nil")
	}
	if walletRepo.findCalls != 1 {
		t.Fatalf("wallet lookup = %d, want 1", walletRepo.findCalls)
	}
	if txRepo.creates != 0 || mockChain.BroadcastTransactionCalls != 0 || mockChain.balanceCalls != 0 {
		t.Fatalf("persist=%d broadcast=%d balance=%d", txRepo.creates, mockChain.BroadcastTransactionCalls, mockChain.balanceCalls)
	}
}

func assertSweepPaused(t *testing.T, err error) {
	t.Helper()
	var gate *features.GateError
	if !errors.As(err, &gate) || gate == nil || gate.Code != features.CodeSweepPaused {
		t.Fatalf("got %v", err)
	}
}

type countingWalletRepo struct {
	fakeWalletRepo
	findCalls int
}

func (c *countingWalletRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	c.findCalls++
	return c.fakeWalletRepo.FindByID(ctx, id)
}

type countingTxRepo struct {
	creates int
}

func (c *countingTxRepo) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("callback is required")
	}
	return fn(ctx)
}

func (c *countingTxRepo) Create(context.Context, *models.Transaction) error {
	c.creates++
	return errors.New("plan must not persist")
}

type balanceCountingChain struct {
	*mocks.MockChain
	balanceCalls int
}

func planFlagService(t *testing.T, walletAccountID uuid.UUID, flags *features.Service) (*service, *countingWalletRepo, *countingTxRepo, *balanceCountingChain) {
	t.Helper()

	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	accountID := walletAccountID
	wallet := &models.Wallet{ID: walletID, Chain: "eth", AccountID: &accountID, DepositAddress: &base}
	mockChain := balanceMapChain("eth", "eth", map[string]*big.Int{"BASE": big.NewInt(1000)})
	mockChain.BroadcastTransactionFn = func(context.Context, *types.SignedTx) (string, error) {
		t.Fatal("broadcast")
		return "", errors.New("broadcast")
	}
	counted := &balanceCountingChain{MockChain: mockChain}
	innerBalance := mockChain.GetBalanceFn
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		counted.balanceCalls++
		return innerBalance(ctx, addr)
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)
	walletRepo := &countingWalletRepo{fakeWalletRepo: fakeWalletRepo{wallet: wallet}}
	txRepo := &countingTxRepo{}
	svc := &service{
		registry:    registry,
		walletRepo:  walletRepo,
		addressRepo: &fakeAddressRepo{children: []models.Address{base}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity("eth")},
		txRepo:      txRepo,
		flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		},
	}
	return svc, walletRepo, txRepo, counted
}

type planFlagStore struct {
	rows   map[uuid.UUID]map[string]bool
	global map[string]bool
}

func newPlanFlagStore() *planFlagStore {
	return &planFlagStore{
		rows:   map[uuid.UUID]map[string]bool{},
		global: map[string]bool{},
	}
}

func (s *planFlagStore) ListAccount(_ context.Context, accountID uuid.UUID) ([]models.Feature, error) {
	values := s.rows[accountID]
	rows := make([]models.Feature, 0, len(values))
	for key, enabled := range values {
		rows = append(rows, models.Feature{AccountID: accountID, Key: key, Enabled: enabled})
	}
	return rows, nil
}

func (s *planFlagStore) Upsert(_ context.Context, accountID uuid.UUID, key string, enabled bool) error {
	if s.rows[accountID] == nil {
		s.rows[accountID] = map[string]bool{}
	}
	s.rows[accountID][key] = enabled
	return nil
}

func (s *planFlagStore) ListGlobal(context.Context) ([]models.GlobalFeature, error) {
	rows := make([]models.GlobalFeature, 0, len(s.global))
	for key, enabled := range s.global {
		rows = append(rows, models.GlobalFeature{Key: key, Enabled: enabled})
	}
	return rows, nil
}

func (s *planFlagStore) GetGlobal(_ context.Context, key string) (bool, bool, error) {
	value, ok := s.global[key]
	return value, ok, nil
}

func (s *planFlagStore) UpsertGlobal(_ context.Context, key string, enabled bool) error {
	if s.global == nil {
		s.global = map[string]bool{}
	}
	s.global[key] = enabled
	return nil
}

type planFlagAdmins struct{}

func (planFlagAdmins) Contains(context.Context, uuid.UUID) (bool, error) { return false, nil }

type planFlagActivity struct{}

func (planFlagActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (planFlagActivity) Append(context.Context, models.AccountActivity) error { return nil }

var _ activitylog.Writer = planFlagActivity{}
var _ features.Store = (*planFlagStore)(nil)
var _ features.PlatformAdmins = planFlagAdmins{}
