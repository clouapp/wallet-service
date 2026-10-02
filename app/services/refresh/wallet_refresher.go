package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// DefaultWalletSpacing paces a full refresh pass: one wallet costs a native balance
// call plus one call per configured token on the same provider.
const DefaultWalletSpacing = 500 * time.Millisecond

type WalletStore interface {
	FindByID(id uuid.UUID) (*models.Wallet, error)
	FindAll() ([]models.Wallet, error)
}

type walletBalanceRefresh interface {
	RefreshWallet(ctx context.Context, wallet *models.Wallet) error
}

// ChainSet tells which chains have a registered adapter.
type ChainSet interface {
	ChainIDs() []string
}

// PassSummary counts what one RefreshAll pass did.
type PassSummary struct {
	Refreshed int
	Failed    int
	Skipped   int
}

// WalletRefresher keeps the wallet balance read model in step with the chain, both on
// a schedule (RefreshAll) and right after a confirmed movement (RefreshWalletByID).
// Refreshes run one at a time: two concurrent ones for a wallet would race on its rows.
type WalletRefresher struct {
	balances walletBalanceRefresh
	wallets  WalletStore
	chains   ChainSet
	spacing  time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
	mu       sync.Mutex
}

func NewWalletRefresher(balances walletBalanceRefresh, wallets WalletStore, chains ChainSet, spacing time.Duration) (*WalletRefresher, error) {
	if balances == nil || wallets == nil || chains == nil {
		return nil, errors.New("wallet refresher: balance service, wallet store and chain set are required")
	}
	if spacing < 0 {
		return nil, fmt.Errorf("wallet refresher: spacing must not be negative, got %s", spacing)
	}
	return &WalletRefresher{balances: balances, wallets: wallets, chains: chains, spacing: spacing, sleep: sleepContext}, nil
}

func (r *WalletRefresher) RefreshWalletByID(ctx context.Context, walletID uuid.UUID) error {
	if walletID == uuid.Nil {
		return errors.New("wallet id is required")
	}
	wallet, err := r.wallets.FindByID(walletID)
	if err != nil {
		return fmt.Errorf("load wallet %s: %w", walletID, err)
	}
	if wallet == nil {
		return fmt.Errorf("wallet %s not found", walletID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.balances.RefreshWallet(ctx, wallet)
}

// RefreshAll refreshes every active wallet of a registered chain, pacing the calls.
// When a provider stays rate limited after the RPC client's own backoff, the rest of
// that chain's wallets wait for the next pass instead of hammering it.
func (r *WalletRefresher) RefreshAll(ctx context.Context) (PassSummary, error) {
	wallets, err := r.wallets.FindAll()
	if err != nil {
		return PassSummary{}, fmt.Errorf("list wallets: %w", err)
	}
	registered := map[string]bool{}
	for _, chainID := range r.chains.ChainIDs() {
		registered[chainID] = true
	}
	rateLimited := map[string]bool{}
	var summary PassSummary
	for i := range wallets {
		wallet := &wallets[i]
		if !isRefreshable(wallet, registered) || rateLimited[wallet.Chain] {
			summary.Skipped++
			continue
		}
		if summary.Refreshed+summary.Failed > 0 {
			if err := r.sleep(ctx, r.spacing); err != nil {
				return summary, err
			}
		}
		if err := r.refreshLocked(ctx, wallet); err != nil {
			summary.Failed++
			if errors.Is(err, chain.ErrRateLimited) {
				rateLimited[wallet.Chain] = true
				slog.Warn("balance refresh rate limited; the chain's other wallets wait for the next pass", "chain", wallet.Chain, "wallet", wallet.ID)
				continue
			}
			slog.Error("balance refresh failed", "chain", wallet.Chain, "wallet", wallet.ID, "error", err)
			continue
		}
		summary.Refreshed++
	}
	return summary, ctx.Err()
}

func (r *WalletRefresher) refreshLocked(ctx context.Context, wallet *models.Wallet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.balances.RefreshWallet(ctx, wallet)
}

func isRefreshable(wallet *models.Wallet, registered map[string]bool) bool {
	return registered[wallet.Chain] && wallet.DepositAddress != nil && wallet.Status != models.WalletStatusArchived
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
