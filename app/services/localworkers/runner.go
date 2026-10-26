// Package localworkers runs, inside the local HTTP server, the jobs that the
// deposit_scanner, confirmation_tracker and webhook_worker Lambdas run in production,
// plus the periodic wallet balance refresh that keeps the read model current, the
// deposit signature sync of the chains deposit_signatures lists and, when it is
// enabled, the real-time XRP deposit stream.
package localworkers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/refresh"
)

const (
	MinInterval       = time.Second
	DeliveryBatchSize = 25
	// RedispatchBatchSize bounds the outbox rows one re-dispatch run sends to the queue.
	RedispatchBatchSize = 50
	// DefaultSignatureSyncTick is how often the signature loop reads the signature
	// sync's settings: a listed chain is synced once its interval elapsed since its
	// last sync started, so a changed setting applies on the next tick, no restart.
	DefaultSignatureSyncTick = 15 * time.Second
)

// dedicatedScanChains produce blocks too fast (Arbitrum about every 0.25 s, Solana a
// slot every 0.4 s, BSC 0.75 s, Base 2 s, TRON 3 s) to wait for the other chains' scans;
// each one runs in a loop of its own. A public Solana endpoint also rate-limits getBlock
// (devnet: 6 per 10 s), so a Solana cycle can take minutes and must not hold up the
// chains of the shared loop.
var dedicatedScanChains = map[string]bool{
	models.ChainBase: true, models.ChainTBase: true,
	models.ChainArbitrum: true, models.ChainTArbitrum: true,
	models.ChainSOL: true, models.ChainTSOL: true,
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

// OutboxRedispatcher sends to the webhook queue again the outbox rows whose queue
// message was lost; it is the queue-mode counterpart of OutboxDeliverer.
type OutboxRedispatcher interface {
	RedispatchStale(ctx context.Context, limit int) (int, error)
}

// DepositScanner is the deposit_scanner Lambda body: scan new blocks of one chain,
// record deposits to watched addresses, then refresh that chain's confirmations.
// ReprocessDuePending retries the chain's pending blocks whose backoff elapsed.
// SyncAddressCache rebuilds the chain's watched-address set when it drifted from the
// database; the local scanner shares Redis with other processes that may overwrite it.
// BackfillDepositEvents publishes again the deposit.confirmed deliveries those
// chains' confirmed deposits never enqueued.
type DepositScanner interface {
	ScanLatestBlocks(ctx context.Context, chainID string) error
	ReprocessDuePending(ctx context.Context, chainID string) (int, error)
	SyncAddressCache(ctx context.Context, chainID string) (bool, error)
	BackfillDepositEvents(ctx context.Context, chainIDs []string) (int, error)
}

// SignatureSyncer is the deposit signature sync: its settings at the moment of use
// (the chains it reads and how often) and one cycle of a chain.
type SignatureSyncer interface {
	SignatureSettings(ctx context.Context) deposit.SignatureSyncSettings
	SyncSignatures(ctx context.Context, chainID string) (deposit.SignatureSyncSummary, error)
}

// BalanceRefresher re-reads every wallet's balances from the chain into the read model.
type BalanceRefresher interface {
	RefreshAll(ctx context.Context) (refresh.PassSummary, error)
}

// PreParamsFiller keeps the MPC keygen pre-parameters generated ahead of time; Run
// returns when ctx is cancelled.
type PreParamsFiller interface {
	Run(ctx context.Context)
}

// DepositStream is a long-running deposit listener of one chain (the XRPL
// WebSocket, app/services/xrpstream); Run returns when ctx is cancelled.
type DepositStream interface {
	Run(ctx context.Context)
}

// Workers are the jobs Start runs; Deliverer, Redispatcher, Scanner, Balances and
// DepositStream are only needed when the configuration enables them. PreParams is
// optional, and so is Signatures, which syncs the deposit scan chains that
// deposit_signatures lists (their block scan pauses meanwhile). ScanChains, when set,
// is read on every supervisor tick and replaces DepositScanChains (which then is only
// the list kept when the first read fails); Streams, when set, runs the deposit stream
// the settings ask for instead of DepositStream; ChainSync runs until ctx ends.
type Workers struct {
	Checker       WithdrawalConfirmationChecker
	Deliverer     OutboxDeliverer
	Redispatcher  OutboxRedispatcher
	Scanner       DepositScanner
	Balances      BalanceRefresher
	PreParams     PreParamsFiller
	DepositStream DepositStream
	Signatures    SignatureSyncer
	ScanChains    ScanChainSource
	Streams       DepositStreams
	ChainSync     ChainSync
}

type Config struct {
	ConfirmationInterval time.Duration
	DeliveryInterval     time.Duration
	// DeliverOutbox is true only when no webhook queue is configured; with a queue the
	// SQS worker owns delivery and the outbox must not deliver a second time.
	DeliverOutbox bool
	// RedispatchOutbox is true only when a webhook queue is configured: on the
	// confirmation interval, outbox rows whose queue message was lost are sent to the
	// queue again. It never runs beside DeliverOutbox.
	RedispatchOutbox bool
	// DepositScanChains opts chains into local block scanning. Their deposits are
	// detected and confirmed, and deposit webhooks are sent, from this machine; the
	// confirmation loop also backfills the deposit.confirmed deliveries they lost.
	DepositScanChains   []string
	DepositScanInterval time.Duration
	// BalanceRefreshInterval paces the wallet balance refresh; 0 turns it off.
	BalanceRefreshInterval time.Duration
	// DepositStreamChain is the chain the deposit stream listens to; empty runs none.
	// It must be one of DepositScanChains: the ledger scan stays the guarantee and
	// confirms what the stream recorded when a scan holds the chain.
	DepositStreamChain string
	// SignatureSyncTick paces the signature loop (0: DefaultSignatureSyncTick). It
	// syncs only DepositScanChains: the process that scans a chain is the one that
	// reads it by signatures instead, so two processes never both read it.
	SignatureSyncTick time.Duration
	// SupervisorTick paces the reads of Workers.ScanChains and Workers.Streams
	// (0: DefaultSupervisorTick).
	SupervisorTick time.Duration
	// KnownChain reports whether the chain registry holds a chain; a scanned chain it
	// does not hold is skipped and logged instead of scanned. nil holds every chain.
	KnownChain func(chainID string) bool
}

func (c Config) validate() error {
	if c.ConfirmationInterval < MinInterval {
		return errors.New("confirmation interval must be at least one second")
	}
	if c.DeliverOutbox && c.DeliveryInterval < MinInterval {
		return errors.New("delivery interval must be at least one second")
	}
	if c.DeliverOutbox && c.RedispatchOutbox {
		return errors.New("the outbox is delivered locally or re-dispatched to a queue, never both")
	}
	if len(c.DepositScanChains) > 0 && c.DepositScanInterval < MinInterval {
		return errors.New("deposit scan interval must be at least one second")
	}
	if c.BalanceRefreshInterval != 0 && c.BalanceRefreshInterval < MinInterval {
		return errors.New("balance refresh interval must be at least one second, or 0 to turn it off")
	}
	if c.SignatureSyncTick != 0 && c.SignatureSyncTick < MinInterval {
		return errors.New("signature sync tick must be at least one second, or 0 for the default")
	}
	if c.SupervisorTick != 0 && c.SupervisorTick < MinInterval {
		return errors.New("supervisor tick must be at least one second, or 0 for the default")
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
	if c.DepositStreamChain != "" && !seen[c.DepositStreamChain] {
		return fmt.Errorf("deposit stream chain %q must also be a deposit scan chain: the ledger scan stays the guarantee", c.DepositStreamChain)
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
	if cfg.RedispatchOutbox && workers.Redispatcher == nil {
		return nil, errors.New("outbox redispatcher is required")
	}
	dynamicChains := workers.ScanChains != nil
	if (len(cfg.DepositScanChains) > 0 || dynamicChains) && scanner == nil {
		return nil, errors.New("deposit scanner is required")
	}
	if dynamicChains && cfg.DepositScanInterval < MinInterval {
		return nil, errors.New("deposit scan interval must be at least one second")
	}
	if cfg.BalanceRefreshInterval > 0 && workers.Balances == nil {
		return nil, errors.New("balance refresher is required")
	}
	if cfg.DepositStreamChain != "" && workers.DepositStream == nil {
		return nil, errors.New("deposit stream is required")
	}
	if workers.Streams != nil && (workers.DepositStream != nil || cfg.DepositStreamChain != "") {
		return nil, errors.New("the deposit stream is either the configured one or the one the settings ask for, never both")
	}
	loops := &Loops{}

	if workers.PreParams != nil {
		loops.running.Go(func() { workers.PreParams.Run(ctx) })
	}
	if workers.ChainSync != nil {
		loops.running.Go(func() { workers.ChainSync.Run(ctx) })
	}

	scans := newScanSupervisor(ctx, loops, scanner, cfg.DepositScanInterval, cfg.KnownChain)
	initial := cfg.DepositScanChains
	if dynamicChains {
		if read, err := workers.ScanChains.ScanChains(ctx); err != nil {
			slog.Error("local deposit scan chains unread; scanning the configured ones", "error", err)
		} else {
			initial = read
		}
	}
	scans.apply(initial)
	if !dynamicChains && cfg.DepositStreamChain != "" {
		loops.running.Go(func() { workers.DepositStream.Run(ctx) })
	}
	streams := &streamSupervisor{streams: workers.Streams, loops: loops}
	streams.reconcile(ctx, scans.current())

	loops.every(ctx, cfg.ConfirmationInterval, func() {
		if err := checker.RunWithdrawalConfirmationCheck(ctx); err != nil {
			slog.Error("local withdrawal confirmation check failed", "error", err)
		}
		if scanChains := scans.current(); len(scanChains) > 0 {
			backfillDepositEvents(ctx, scanner, scanChains)
		}
	})

	if cfg.RedispatchOutbox {
		loops.every(ctx, cfg.ConfirmationInterval, func() {
			redispatchOutbox(ctx, workers.Redispatcher)
		})
	}

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

	scanning := scanner != nil && (dynamicChains || len(scans.current()) > 0)
	if scanning {
		loops.every(ctx, cfg.DepositScanInterval, func() {
			for _, chainID := range scans.shared() {
				if ctx.Err() != nil {
					return
				}
				scanChain(ctx, scanner, chainID)
			}
		})
	}

	supervisorTick := cfg.SupervisorTick
	if supervisorTick == 0 {
		supervisorTick = DefaultSupervisorTick
	}
	if dynamicChains || workers.Streams != nil {
		loops.every(ctx, supervisorTick, func() {
			if dynamicChains {
				if read, err := workers.ScanChains.ScanChains(ctx); err != nil {
					slog.Warn("local deposit scan chains unread; the running ones are kept", "error", err)
				} else {
					scans.apply(read)
				}
			}
			streams.reconcile(ctx, scans.current())
		})
	}

	signatureTick := cfg.SignatureSyncTick
	if signatureTick == 0 {
		signatureTick = DefaultSignatureSyncTick
	}
	signatureSync := workers.Signatures != nil && scanning
	if signatureSync {
		schedule := newLiveSignatureSchedule(scans.current)
		loops.every(ctx, signatureTick, func() {
			for _, chainID := range schedule.due(time.Now(), workers.Signatures.SignatureSettings(ctx)) {
				if ctx.Err() != nil {
					return
				}
				syncSignatures(ctx, workers.Signatures, chainID)
			}
		})
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
		"redispatch_outbox", cfg.RedispatchOutbox,
		"delivery_interval", cfg.DeliveryInterval.String(),
		"deposit_scan_chains", strings.Join(scans.current(), ","),
		"deposit_scan_chains_live", dynamicChains,
		"deposit_scan_interval", cfg.DepositScanInterval.String(),
		"balance_refresh_interval", cfg.BalanceRefreshInterval.String(),
		"deposit_stream_chain", cfg.DepositStreamChain,
		"deposit_stream_live", workers.Streams != nil,
		"chain_sync", workers.ChainSync != nil,
		"signature_sync", signatureSync,
		"signature_sync_tick", signatureTick.String(),
	)
	return loops, nil
}

// signatureSchedule decides, on each tick of the signature loop, which chains to
// sync: those the settings list that this process scans, once their interval elapsed
// since their last sync started (at once the first time). It is used by the loop's
// goroutine only.
type signatureSchedule struct {
	scanned func() []string
	last    map[string]time.Time
	ignored map[string]bool
}

func newSignatureSchedule(scanned []string) *signatureSchedule {
	return newLiveSignatureSchedule(func() []string { return scanned })
}

// newLiveSignatureSchedule reads the scanned chains on each tick.
func newLiveSignatureSchedule(scanned func() []string) *signatureSchedule {
	return &signatureSchedule{scanned: scanned, last: map[string]time.Time{}, ignored: map[string]bool{}}
}

// due returns the chains to sync at now, in the settings' order, and marks them
// started. A listed chain this process does not scan is logged once and left to the
// process that scans it.
func (s *signatureSchedule) due(now time.Time, settings deposit.SignatureSyncSettings) []string {
	var due []string
	scanned := s.scanned()
	for _, chainID := range settings.Chains {
		if !slices.Contains(scanned, chainID) {
			if !s.ignored[chainID] {
				s.ignored[chainID] = true
				slog.Info("deposit signature sync of a chain this process does not scan is left to the process that scans it",
					"chain", chainID, "hint", "list it in the local_workers setting (or LOCAL_DEPOSIT_SCAN_CHAINS) of the process that should read it")
			}
			continue
		}
		if last, ran := s.last[chainID]; ran && now.Sub(last) < settings.Interval {
			continue
		}
		s.last[chainID] = now
		due = append(due, chainID)
	}
	return due
}

func syncSignatures(ctx context.Context, syncer SignatureSyncer, chainID string) {
	summary, err := syncer.SyncSignatures(ctx, chainID)
	if err != nil {
		slog.Error("local deposit signature sync failed", "chain", chainID, "error", err)
		return
	}
	if summary.FailedAccounts > 0 {
		slog.Warn("local deposit signature sync left accounts for the next cycle", "chain", chainID,
			"failed_accounts", summary.FailedAccounts, "accounts", summary.Accounts)
	}
}

// backfillDepositEvents repairs only the chains this machine confirms deposits for;
// the others are confirmed, and backfilled, by the deployed confirmation_tracker.
func backfillDepositEvents(ctx context.Context, scanner DepositScanner, chainIDs []string) {
	republished, err := scanner.BackfillDepositEvents(ctx, chainIDs)
	if err != nil {
		slog.Error("local deposit event backfill failed", "error", err)
		return
	}
	if republished > 0 {
		slog.Info("local deposit events backfilled", "count", republished)
	}
}

func redispatchOutbox(ctx context.Context, redispatcher OutboxRedispatcher) {
	sent, err := redispatcher.RedispatchStale(ctx, RedispatchBatchSize)
	if err != nil {
		slog.Error("local webhook re-dispatch failed", "error", err)
		return
	}
	if sent > 0 {
		slog.Info("local webhooks re-dispatched to the queue", "count", sent)
	}
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
