package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

type fakeWalletStore struct {
	wallets []models.Wallet
	listErr error
}

func (s *fakeWalletStore) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	for i := range s.wallets {
		if s.wallets[i].ID == id {
			return &s.wallets[i], nil
		}
	}
	return nil, nil
}

func (s *fakeWalletStore) FindAll(context.Context) ([]models.Wallet, error) {
	return append([]models.Wallet(nil), s.wallets...), s.listErr
}

type fakeChains []string

func (c fakeChains) ChainIDs() []string { return c }

type recordingBalances struct {
	mu        sync.Mutex
	refreshed []string
	failures  map[string]error
	inFlight  int
	peak      int
}

func (b *recordingBalances) RefreshWallet(_ context.Context, wallet *models.Wallet) error {
	b.mu.Lock()
	b.inFlight++
	b.peak = max(b.peak, b.inFlight)
	b.mu.Unlock()
	time.Sleep(time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inFlight--
	b.refreshed = append(b.refreshed, wallet.Label)
	return b.failures[wallet.Label]
}

func walletOn(chainID, label string) models.Wallet {
	addressID := uuid.New()
	return models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: label, Status: "active", DepositAddressID: &addressID,
		DepositAddress: &models.Address{ID: addressID, Chain: chainID, Address: label + "-base"},
	}
}

type recordedPauses struct{ delays []time.Duration }

func (p *recordedPauses) sleep(_ context.Context, d time.Duration) error {
	p.delays = append(p.delays, d)
	return nil
}

func newTestRefresher(t *testing.T, balances *recordingBalances, wallets *fakeWalletStore, chains fakeChains) (*WalletRefresher, *recordedPauses) {
	t.Helper()
	refresher, err := NewWalletRefresher(balances, wallets, chains, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	pauses := &recordedPauses{}
	refresher.sleep = pauses.sleep
	return refresher, pauses
}

func TestNewWalletRefresher_ValidatesDependencies(t *testing.T) {
	if _, err := NewWalletRefresher(nil, &fakeWalletStore{}, fakeChains{}, 0); err == nil {
		t.Fatal("expected a missing balance service to be rejected")
	}
	if _, err := NewWalletRefresher(&recordingBalances{}, &fakeWalletStore{}, fakeChains{}, -time.Second); err == nil {
		t.Fatal("expected a negative spacing to be rejected")
	}
}

func TestRefreshAll_RefreshesEveryRegisteredChainAndPacesTheCalls(t *testing.T) {
	archived := walletOn("eth", "eth_archived")
	archived.Status = models.WalletStatusArchived
	noAddress := walletOn("sol", "sol_no_address")
	noAddress.DepositAddress = nil
	wallets := &fakeWalletStore{wallets: []models.Wallet{
		walletOn("eth", "eth_deposit"), walletOn("btc", "btc_deposit"), walletOn("sol", "sol_withdraw"),
		walletOn("unregistered", "orphan"), archived, noAddress,
	}}
	balances := &recordingBalances{failures: map[string]error{"btc_deposit": errors.New("esplora GET /address: HTTP 502")}}
	refresher, pauses := newTestRefresher(t, balances, wallets, fakeChains{"eth", "btc", "sol"})

	summary, err := refresher.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (PassSummary{Refreshed: 2, Failed: 1, Skipped: 3}) {
		t.Fatalf("unexpected summary %+v", summary)
	}
	if got := strings.Join(balances.refreshed, ","); got != "eth_deposit,btc_deposit,sol_withdraw" {
		t.Fatalf("a failed wallet must not stop the others, refreshed %s", got)
	}
	if got := fmt.Sprint(pauses.delays); got != "[250ms 250ms]" {
		t.Fatalf("expected a pause between wallets only, got %s", got)
	}
}

func TestRefreshAll_RateLimitedChainWaitsForTheNextPass(t *testing.T) {
	wallets := &fakeWalletStore{wallets: []models.Wallet{
		walletOn("sol", "sol_deposit"), walletOn("sol", "sol_withdraw"), walletOn("eth", "eth_deposit"),
	}}
	rateLimited := fmt.Errorf("get native balance: rpc call getBalance: %w (HTTP 429) after 6 attempts", chain.ErrRateLimited)
	balances := &recordingBalances{failures: map[string]error{"sol_deposit": rateLimited}}
	refresher, _ := newTestRefresher(t, balances, wallets, fakeChains{"sol", "eth"})

	summary, err := refresher.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(balances.refreshed, ","); got != "sol_deposit,eth_deposit" {
		t.Fatalf("the rate-limited chain's other wallets must be skipped, refreshed %s", got)
	}
	if summary != (PassSummary{Refreshed: 1, Failed: 1, Skipped: 1}) {
		t.Fatalf("unexpected summary %+v", summary)
	}
}

func TestRefreshAll_ReportsAWalletListFailure(t *testing.T) {
	refresher, _ := newTestRefresher(t, &recordingBalances{}, &fakeWalletStore{listErr: errors.New("db down")}, fakeChains{"eth"})
	if _, err := refresher.RefreshAll(context.Background()); err == nil {
		t.Fatal("expected the wallet list failure")
	}
}

func TestRefreshWalletByID(t *testing.T) {
	target := walletOn("eth", "eth_withdraw")
	balances := &recordingBalances{}
	refresher, _ := newTestRefresher(t, balances, &fakeWalletStore{wallets: []models.Wallet{target}}, fakeChains{"eth"})

	if err := refresher.RefreshWalletByID(context.Background(), target.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(balances.refreshed, ",") != "eth_withdraw" {
		t.Fatalf("expected the wallet refreshed, got %v", balances.refreshed)
	}
	if err := refresher.RefreshWalletByID(context.Background(), uuid.Nil); err == nil {
		t.Fatal("expected a nil id to be rejected")
	}
	if err := refresher.RefreshWalletByID(context.Background(), uuid.New()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected an unknown wallet to be reported, got %v", err)
	}
}

func TestWalletRefresher_SerializesRefreshes(t *testing.T) {
	wallets := &fakeWalletStore{wallets: []models.Wallet{walletOn("eth", "a"), walletOn("eth", "b"), walletOn("eth", "c")}}
	balances := &recordingBalances{}
	refresher, _ := newTestRefresher(t, balances, wallets, fakeChains{"eth"})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = refresher.RefreshAll(context.Background())
	}()
	for _, wallet := range wallets.wallets {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_ = refresher.RefreshWalletByID(context.Background(), id)
		}(wallet.ID)
	}
	wg.Wait()
	if balances.peak != 1 {
		t.Fatalf("refreshes must never overlap, peak was %d", balances.peak)
	}
}
