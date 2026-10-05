// Package localworkers runs, inside the local HTTP server, the jobs that the
// deposit_scanner, confirmation_tracker and webhook_worker Lambdas run in production,
// plus the periodic wallet balance refresh that keeps the read model current.
package localworkers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/refresh"
)

const (
	MinInterval       = time.Second
	DeliveryBatchSize = 25
)

// dedicatedScanChains produce blocks too fast (Arbitrum about every 0.25 s, BSC 0.75 s,
// Base 2 s, TRON 3 s) to wait for the other chains' scans; each one runs in a loop of
// its own.
var dedicatedScanChains = map[string]bool{
	models.ChainBase: true, models.ChainTBase: true,
	models.ChainArbitrum: true, models.ChainTArbitrum: true,
	models.ChainBSC: true, models.ChainTBSC: true,
	models.ChainTron: true, models.ChainTTron: true,
}

// splitScanChains keeps the configured order: shared chains are scanned one after the
// other in one loop, dedicated chains each in their own loop.
func splitScanChains(chainIDs []string) (shared, dedicated []string) {
	for _, chainID := range chainIDs {
		if dedicatedScanChains[chainID] {
			dedicated = append(dedicated, chainID)
			continue
		}
		shared = append(shared, chainID)
	}
	return shared, dedicated
}

type WithdrawalConfirmationChecker interface {
	RunWithdrawalConfirmationCheck(ctx context.Context) error
}

type OutboxDeliverer interface {
	DeliverPending(ctx context.Context, limit int) (int, error)
}

// DepositScanner is the deposit_scanner Lambda body: scan new blocks of one chain,
// record deposits to watched addresses, then refresh that chain's confirmations.
// ReprocessDuePending retries the chain's pending blocks whose backoff elapsed.
// SyncAddressCache rebuilds the chain's watched-address set when it drifted from the
// database; the local scanner shares Redis with other processes that may overwrite it.
type DepositScanner interface {
	ScanLatestBlocks(ctx context.Context, chainID string) error
	ReprocessDuePending(ctx context.Context, chainID string) (int, error)
	SyncAddressCache(ctx context.Context, chainID string) (bool, error)
}

// BalanceRefresher re-reads every wallet's balances from the chain into the read model.
type BalanceRefresher interface {
	RefreshAll(ctx context.Context) (refresh.PassSummary, error)
}

// Workers are the jobs Start runs; Deliverer, Scanner and Balances are only needed
// when the configuration enables their loop.
type Workers struct {
	Checker   WithdrawalConfirmationChecker
	Deliverer OutboxDeliverer
	Scanner   DepositScanner
	Balances  BalanceRefresher
}

type Config struct {
	ConfirmationInterval time.Duration
	DeliveryInterval     time.Duration
	// DeliverOutbox is true only when no webhook queue is configured; with a queue the
	// SQS worker owns delivery and the outbox must not deliver a second time.
	DeliverOutbox bool
	// DepositScanChains opts chains into local block scanning. Their deposits are
	// detected and confirmed, and deposit webhooks are sent, from this machine.
	DepositScanChains   []string
	DepositScanInterval time.Duration
	// BalanceRefreshInterval paces the wallet balance refresh; 0 turns it off.
	BalanceRefreshInterval time.Duration
}

func (c Config) validate() error {
	if c.ConfirmationInterval < MinInterval {
		return errors.New("confirmation interval must be at least one second")
	}
	if c.DeliverOutbox && c.DeliveryInterval < MinInterval {
		return errors.New("delivery interval must be at least one second")
	}
	if len(c.DepositScanChains) > 0 && c.DepositScanInterval < MinInterval {
		return errors.New("deposit scan interval must be at least one second")
	}
	if c.BalanceRefreshInterval != 0 && c.BalanceRefreshInterval < MinInterval {
		return errors.New("balance refresh interval must be at least one second, or 0 to turn it off")
	}
	seen := make(map[string]bool, len(c.DepositScanChains))
	for _, chainID := range c.DepositScanChains {
		if chainID == "" || chainID != strings.TrimSpace(chainID) {
			return fmt.Errorf("invalid deposit scan chain %q", chainID)
		}
		if seen[chainID] {
			return fmt.Errorf("deposit scan chain %q listed twice", chainID)
		}
		seen[chainID] = true
	}
	return nil
}

// ParseChainList splits a comma-separated chain list, ignoring blanks and spaces.
func ParseChainList(raw string) []string {
	var chains []string
	for _, part := range strings.Split(raw, ",") {
		if chainID := strings.TrimSpace(part); chainID != "" {
			chains = append(chains, chainID)
		}
	}
	return chains
}

// Loops are the background loops launched by Start.
type Loops struct{ running sync.WaitGroup }

// Wait blocks until every loop returned: after ctx is cancelled, each loop finishes
// the run in progress, if any, and returns.
func (l *Loops) Wait() {
	if l == nil {
		return
	}
	l.running.Wait()
}

func (l *Loops) every(ctx context.Context, interval time.Duration, run func()) {
	l.running.Add(1)
	go func() {
		defer l.running.Done()
		every(ctx, interval, run)
	}()
}

// Start launches the background loops; they stop when ctx is cancelled.
func Start(ctx context.Context, cfg Config, workers Workers) (*Loops, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	checker, deliverer, scanner := workers.Checker, workers.Deliverer, workers.Scanner
	if checker == nil {
		return nil, errors.New("withdrawal confirmation checker is required")
	}
	if cfg.DeliverOutbox && deliverer == nil {
		return nil, errors.New("outbox deliverer is required")
	}
	if len(cfg.DepositScanChains) > 0 && scanner == nil {
		return nil, errors.New("deposit scanner is required")
	}
	if cfg.BalanceRefreshInterval > 0 && workers.Balances == nil {
		return nil, errors.New("balance refresher is required")
	}
	scanChains := append([]string(nil), cfg.DepositScanChains...)
	loops := &Loops{}

	loops.every(ctx, cfg.ConfirmationInterval, func() {
		if err := checker.RunWithdrawalConfirmationCheck(ctx); err != nil {
			slog.Error("local withdrawal confirmation check failed", "error", err)
		}
	})

	if cfg.DeliverOutbox {
		loops.every(ctx, cfg.DeliveryInterval, func() {
			delivered, err := deliverer.DeliverPending(ctx, DeliveryBatchSize)
			if err != nil {
				slog.Error("local webhook delivery failed", "error", err)
				return
			}
			if delivered > 0 {
				slog.Info("local webhooks delivered", "count", delivered)
			}
		})
	}

	if len(scanChains) > 0 {
		for _, chainID := range scanChains {
			syncAddressCache(ctx, scanner, chainID)
		}
		sharedChains, dedicatedChains := splitScanChains(scanChains)
		if len(sharedChains) > 0 {
			loops.every(ctx, cfg.DepositScanInterval, func() {
				for _, chainID := range sharedChains {
					if ctx.Err() != nil {
						return
					}
					scanChain(ctx, scanner, chainID)
				}
			})
		}
		for _, chainID := range dedicatedChains {
			loops.every(ctx, cfg.DepositScanInterval, func() {
				scanChain(ctx, scanner, chainID)
			})
		}
	}

	if cfg.BalanceRefreshInterval > 0 {
		loops.every(ctx, cfg.BalanceRefreshInterval, func() {
			summary, err := workers.Balances.RefreshAll(ctx)
			if err != nil {
				slog.Error("local balance refresh failed", "error", err, "refreshed", summary.Refreshed, "failed", summary.Failed)
				return
			}
			slog.Info("local balance refresh complete", "refreshed", summary.Refreshed, "failed", summary.Failed, "skipped", summary.Skipped)
		})
	}

	slog.Info("local workers started",
		"confirmation_interval", cfg.ConfirmationInterval.String(),
		"deliver_outbox", cfg.DeliverOutbox,
		"delivery_interval", cfg.DeliveryInterval.String(),
		"deposit_scan_chains", strings.Join(scanChains, ","),
		"deposit_scan_interval", cfg.DepositScanInterval.String(),
		"balance_refresh_interval", cfg.BalanceRefreshInterval.String(),
	)
	return loops, nil
}

func scanChain(ctx context.Context, scanner DepositScanner, chainID string) {
	syncAddressCache(ctx, scanner, chainID)
	if err := scanner.ScanLatestBlocks(ctx, chainID); err != nil {
		slog.Error("local deposit scan failed", "chain", chainID, "error", err)
	}
	reprocessPending(ctx, scanner, chainID)
}

// syncAddressCache logs instead of failing: when Redis is unreachable the scanner
// checks addresses against the database, so the scan still runs.
func syncAddressCache(ctx context.Context, scanner DepositScanner, chainID string) {
	if _, err := scanner.SyncAddressCache(ctx, chainID); err != nil {
		slog.Error("local address cache sync failed", "chain", chainID, "error", err)
	}
}

// reprocessPending runs after the scan in the same loop, so a chain's pending blocks
// and its new blocks are never recorded at the same time.
func reprocessPending(ctx context.Context, scanner DepositScanner, chainID string) {
	resolved, err := scanner.ReprocessDuePending(ctx, chainID)
	if err != nil {
		slog.Error("local pending deposit reprocess failed", "chain", chainID, "error", err)
		return
	}
	if resolved > 0 {
		slog.Info("local pending deposit blocks recovered", "chain", chainID, "count", resolved)
	}
}

// every waits one interval before the first run, so a server that fails to bind
// its port exits before any worker touches the chain or the outbox.
func every(ctx context.Context, interval time.Duration, run func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			return
		}
		run()
	}
}
