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
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	FindAll(ctx context.Context) ([]models.Wallet, error)
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

// WalletRefresherDeps is everything the wallet refresher needs. A nil field
// means that dependency is absent. Spacing is the pause between wallets; zero
// is a valid pause and a negative value is rejected.
type WalletRefresherDeps struct {
	Balances walletBalanceRefresh
	Wallets  WalletStore
	Chains   ChainSet
	Spacing  time.Duration
}

// NewWalletRefresher wires the wallet refresher from WalletRefresherDeps.
func NewWalletRefresher(deps WalletRefresherDeps) (*WalletRefresher, error) {
	if deps.Balances == nil || deps.Wallets == nil || deps.Chains == nil {
		return nil, errors.New("wallet refresher: balance service, wallet store and chain set are required")
	}
	if deps.Spacing < 0 {
		return nil, fmt.Errorf("wallet refresher: spacing must not be negative, got %s", deps.Spacing)
	}
	return &WalletRefresher{balances: deps.Balances, wallets: deps.Wallets, chains: deps.Chains, spacing: deps.Spacing, sleep: sleepContext}, nil
}

func (r *WalletRefresher) RefreshWalletByID(ctx context.Context, walletID uuid.UUID) error {
	if walletID == uuid.Nil {
		return errors.New("wallet id is required")
	}
	wallet, err := r.wallets.FindByID(ctx, walletID)
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

// RefreshBalances loads the queued wallet, refuses a chain that does not match,
// and refreshes its balances.
func (r *WalletRefresher) RefreshBalances(ctx context.Context, walletID uuid.UUID, chainID string) error {
	return r.refreshQueued(ctx, walletID, chainID, "refresh_wallet_balances")
}

// RefreshTransactions loads the queued wallet and refreshes it. Transactions are
// refreshed as part of the balance sync for now.
func (r *WalletRefresher) RefreshTransactions(ctx context.Context, walletID uuid.UUID, chainID string) error {
	return r.refreshQueued(ctx, walletID, chainID, "refresh_wallet_transactions")
}

// RefreshTokens loads the queued wallet and refreshes it. Token balances are
// refreshed as part of BalanceService.RefreshWallet.
func (r *WalletRefresher) RefreshTokens(ctx context.Context, walletID uuid.UUID, chainID string) error {
	return r.refreshQueued(ctx, walletID, chainID, "refresh_wallet_tokens")
}

// ReconcileWallet loads the queued wallet and reconciles it. Full reconciliation
// compares chain state with the database; for now it runs a fresh balance sync.
func (r *WalletRefresher) ReconcileWallet(ctx context.Context, walletID uuid.UUID, chainID string) error {
	return r.refreshQueued(ctx, walletID, chainID, "reconcile_wallet_state")
}

// RefreshUTXOs loads the queued wallet. Non-Bitcoin chains are skipped. Fetching
// UTXOs from the chain is not implemented yet.
func (r *WalletRefresher) RefreshUTXOs(ctx context.Context, walletID uuid.UUID, chainID string) error {
	const job = "refresh_wallet_utxos"
	wallet, err := r.walletForJob(ctx, walletID, chainID, job)
	if err != nil {
		return err
	}
	if wallet.Chain != models.ChainBTC && wallet.Chain != models.ChainTBTC {
		slog.Warn("refresh_wallet_utxos: skipping non-Bitcoin chain", "wallet", walletID.String(), "chain", chainID)
		return nil
	}
	slog.Info("refresh_wallet_utxos: UTXO fetch from chain not yet implemented, infrastructure ready",
		"wallet", walletID.String(), "chain", chainID)
	return nil
}

func (r *WalletRefresher) refreshQueued(ctx context.Context, walletID uuid.UUID, chainID, job string) error {
	if r == nil || r.balances == nil {
		return fmt.Errorf("%s: balance refresh service is not initialized", job)
	}
	wallet, err := r.walletForJob(ctx, walletID, chainID, job)
	if err != nil {
		return err
	}
	slog.Info(job, "wallet", walletID.String(), "chain", chainID)
	if err := r.refreshLocked(ctx, wallet); err != nil {
		return fmt.Errorf("%s: %w", job, err)
	}
	return nil
}

func (r *WalletRefresher) walletForJob(ctx context.Context, walletID uuid.UUID, chainID, job string) (*models.Wallet, error) {
	if r == nil || r.wallets == nil {
		return nil, fmt.Errorf("%s: balance refresh service is not initialized", job)
	}
	wallet, err := r.wallets.FindByID(ctx, walletID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("%s: load wallet: %w", job, err)
	}
	if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("%s: wallet not found: %s", job, walletID)
	}
	if wallet.Chain != chainID {
		return nil, fmt.Errorf("%s: chain_id %q does not match wallet chain %q", job, chainID, wallet.Chain)
	}
	return wallet, nil
}

// RefreshAll refreshes every active wallet of a registered chain, pacing the calls.
// When a provider stays rate limited after the RPC client's own backoff, the rest of
// that chain's wallets wait for the next pass instead of hammering it.
func (r *WalletRefresher) RefreshAll(ctx context.Context) (PassSummary, error) {
	wallets, err := r.wallets.FindAll(ctx)
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
