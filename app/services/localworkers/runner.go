// Package localworkers runs, inside the local HTTP server, the jobs that the
// deposit_scanner, confirmation_tracker and webhook_worker Lambdas run in production.
package localworkers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	MinInterval       = time.Second
	DeliveryBatchSize = 25
)

type WithdrawalConfirmationChecker interface {
	RunWithdrawalConfirmationCheck(ctx context.Context) error
}

type OutboxDeliverer interface {
	DeliverPending(ctx context.Context, limit int) (int, error)
}

// DepositScanner is the deposit_scanner Lambda body: scan new blocks of one chain,
// record deposits to watched addresses, then refresh that chain's confirmations.
// SyncAddressCache rebuilds the chain's watched-address set when it drifted from the
// database; the local scanner shares Redis with other processes that may overwrite it.
type DepositScanner interface {
	ScanLatestBlocks(ctx context.Context, chainID string) error
	SyncAddressCache(ctx context.Context, chainID string) (bool, error)
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

// Start launches the background loops; they stop when ctx is cancelled.
func Start(ctx context.Context, cfg Config, checker WithdrawalConfirmationChecker, deliverer OutboxDeliverer, scanner DepositScanner) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if checker == nil {
		return errors.New("withdrawal confirmation checker is required")
	}
	if cfg.DeliverOutbox && deliverer == nil {
		return errors.New("outbox deliverer is required")
	}
	if len(cfg.DepositScanChains) > 0 && scanner == nil {
		return errors.New("deposit scanner is required")
	}
	scanChains := append([]string(nil), cfg.DepositScanChains...)

	go every(ctx, cfg.ConfirmationInterval, func() {
		if err := checker.RunWithdrawalConfirmationCheck(ctx); err != nil {
			slog.Error("local withdrawal confirmation check failed", "error", err)
		}
	})

	if cfg.DeliverOutbox {
		go every(ctx, cfg.DeliveryInterval, func() {
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
		go every(ctx, cfg.DepositScanInterval, func() {
			for _, chainID := range scanChains {
				syncAddressCache(ctx, scanner, chainID)
				if err := scanner.ScanLatestBlocks(ctx, chainID); err != nil {
					slog.Error("local deposit scan failed", "chain", chainID, "error", err)
				}
			}
		})
	}

	slog.Info("local workers started",
		"confirmation_interval", cfg.ConfirmationInterval.String(),
		"deliver_outbox", cfg.DeliverOutbox,
		"delivery_interval", cfg.DeliveryInterval.String(),
		"deposit_scan_chains", strings.Join(scanChains, ","),
		"deposit_scan_interval", cfg.DepositScanInterval.String(),
	)
	return nil
}

// syncAddressCache logs instead of failing: when Redis is unreachable the scanner
// checks addresses against the database, so the scan still runs.
func syncAddressCache(ctx context.Context, scanner DepositScanner, chainID string) {
	if _, err := scanner.SyncAddressCache(ctx, chainID); err != nil {
		slog.Error("local address cache sync failed", "chain", chainID, "error", err)
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
		run()
	}
}
