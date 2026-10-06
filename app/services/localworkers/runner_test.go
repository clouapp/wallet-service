package localworkers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/refresh"
)

type countingChecker struct{ runs atomic.Int32 }

func (c *countingChecker) RunWithdrawalConfirmationCheck(context.Context) error {
	c.runs.Add(1)
	return nil
}

type countingDeliverer struct{ runs atomic.Int32 }

func (d *countingDeliverer) DeliverPending(context.Context, int) (int, error) {
	d.runs.Add(1)
	return 0, nil
}

func TestStart_Rejects_InvalidConfiguration(t *testing.T) {
	checker := &countingChecker{}
	deliverer := &countingDeliverer{}
	cases := map[string]struct {
		cfg       Config
		checker   WithdrawalConfirmationChecker
		deliverer OutboxDeliverer
	}{
		"confirmation interval too short": {Config{ConfirmationInterval: time.Millisecond}, checker, nil},
		"delivery interval too short":     {Config{ConfirmationInterval: MinInterval, DeliverOutbox: true}, checker, deliverer},
		"missing checker":                 {Config{ConfirmationInterval: MinInterval}, nil, nil},
		"outbox without deliverer":        {Config{ConfirmationInterval: MinInterval, DeliveryInterval: MinInterval, DeliverOutbox: true}, checker, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Start(context.Background(), tc.cfg, Workers{Checker: tc.checker, Deliverer: tc.deliverer}); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestStart_Waits_OneIntervalThenRunsBothLoops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker := &countingChecker{}
	deliverer := &countingDeliverer{}
	cfg := Config{ConfirmationInterval: MinInterval, DeliveryInterval: MinInterval, DeliverOutbox: true}

	if _, err := Start(ctx, cfg, Workers{Checker: checker, Deliverer: deliverer}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(MinInterval / 2)
	if checker.runs.Load() != 0 || deliverer.runs.Load() != 0 {
		t.Fatalf("workers must not run before the first interval, got checker=%d deliverer=%d", checker.runs.Load(), deliverer.runs.Load())
	}

	deadline := time.Now().Add(3 * MinInterval)
	for time.Now().Before(deadline) && (checker.runs.Load() == 0 || deliverer.runs.Load() == 0) {
		time.Sleep(50 * time.Millisecond)
	}
	if checker.runs.Load() == 0 || deliverer.runs.Load() == 0 {
		t.Fatalf("expected both loops to run, got checker=%d deliverer=%d", checker.runs.Load(), deliverer.runs.Load())
	}
}

func TestStart_Skips_OutboxWhenAQueueDelivers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker := &countingChecker{}
	deliverer := &countingDeliverer{}

	if _, err := Start(ctx, Config{ConfirmationInterval: MinInterval}, Workers{Checker: checker, Deliverer: deliverer}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(MinInterval + MinInterval/2)
	if deliverer.runs.Load() != 0 {
		t.Fatalf("outbox must not be delivered when a queue is configured, got %d runs", deliverer.runs.Load())
	}
	if checker.runs.Load() == 0 {
		t.Fatal("expected the confirmation loop to run")
	}
}

type recordingScanner struct {
	mu         sync.Mutex
	chains     []string
	synced     []string
	steps      []string
	err        error
	syncErr    error
	pendingErr error
}

func (s *recordingScanner) ScanLatestBlocks(_ context.Context, chainID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains = append(s.chains, chainID)
	s.steps = append(s.steps, "scan:"+chainID)
	return s.err
}

func (s *recordingScanner) ReprocessDuePending(_ context.Context, chainID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, "pending:"+chainID)
	return 0, s.pendingErr
}

func (s *recordingScanner) recordedSteps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

func (s *recordingScanner) SyncAddressCache(_ context.Context, chainID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.synced = append(s.synced, chainID)
	return false, s.syncErr
}

func (s *recordingScanner) scanned() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.chains...)
}

func (s *recordingScanner) cacheSyncs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.synced...)
}

func TestStart_Syncs_AddressCachesBeforeTheFirstScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{syncErr: errors.New("redis down")}
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"sol", "eth", "btc"}, DepositScanInterval: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner}); err != nil {
		t.Fatalf("Start must not fail when a cache sync fails: %v", err)
	}
	if got := scanner.cacheSyncs(); len(got) != 3 || got[0] != "sol" || got[1] != "eth" || got[2] != "btc" {
		t.Fatalf("expected every scan chain synced on start, got %v", got)
	}
	if got := scanner.scanned(); len(got) != 0 {
		t.Fatalf("no scan may run on start, got %v", got)
	}

	deadline := time.Now().Add(3 * MinInterval)
	for time.Now().Before(deadline) && len(scanner.scanned()) < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := scanner.scanned(); len(got) < 3 {
		t.Fatalf("scans must keep running after a failed cache sync, got %v", got)
	}
	if got := scanner.cacheSyncs(); len(got) < 6 {
		t.Fatalf("each scan tick must re-sync the cache first, got %v", got)
	}
}

func TestStart_Rejects_InvalidDepositScanConfiguration(t *testing.T) {
	checker := &countingChecker{}
	scanner := &recordingScanner{}
	base := Config{ConfirmationInterval: MinInterval, DepositScanInterval: MinInterval}
	cases := map[string]struct {
		chains   []string
		interval time.Duration
		scanner  DepositScanner
	}{
		"scan interval too short": {[]string{"sol"}, time.Millisecond, scanner},
		"missing scanner":         {[]string{"sol"}, MinInterval, nil},
		"blank chain":             {[]string{""}, MinInterval, scanner},
		"padded chain":            {[]string{" sol"}, MinInterval, scanner},
		"duplicate chain":         {[]string{"sol", "sol"}, MinInterval, scanner},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.DepositScanChains = tc.chains
			cfg.DepositScanInterval = tc.interval
			if _, err := Start(context.Background(), cfg, Workers{Checker: checker, Scanner: tc.scanner}); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestStart_Scans_EveryConfiguredChainAndKeepsGoingAfterErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{err: errors.New("rate limited")}
	chains := []string{"sol", "tsol"}
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: chains, DepositScanInterval: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	chains[0] = "mutated"

	time.Sleep(MinInterval / 2)
	if got := scanner.scanned(); len(got) != 0 {
		t.Fatalf("scanner must not run before the first interval, got %v", got)
	}
	deadline := time.Now().Add(4 * MinInterval)
	for time.Now().Before(deadline) && len(scanner.scanned()) < 4 {
		time.Sleep(50 * time.Millisecond)
	}
	got := scanner.scanned()
	if len(got) < 4 {
		t.Fatalf("expected two rounds over both chains, got %v", got)
	}
	for i, chainID := range got[:4] {
		if want := []string{"sol", "tsol"}[i%2]; chainID != want {
			t.Fatalf("scan %d chain %q, want %q (all: %v)", i, chainID, want, got)
		}
	}
}

func TestStart_No_ScanWithoutChains(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{}
	if _, err := Start(ctx, Config{ConfirmationInterval: MinInterval, DepositScanInterval: MinInterval}, Workers{Checker: &countingChecker{}, Scanner: scanner}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(MinInterval + MinInterval/2)
	if got := scanner.scanned(); len(got) != 0 {
		t.Fatalf("no chain configured, got scans %v", got)
	}
}

func TestStart_Reprocesses_PendingBlocksAfterEachChainScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{err: errors.New("rpc down"), pendingErr: errors.New("redis and file down")}
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"sol", "btc"}, DepositScanInterval: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(4 * MinInterval)
	for time.Now().Before(deadline) && len(scanner.recordedSteps()) < 8 {
		time.Sleep(50 * time.Millisecond)
	}
	got := scanner.recordedSteps()
	want := []string{"scan:sol", "pending:sol", "scan:btc", "pending:btc"}
	if len(got) < 8 {
		t.Fatalf("expected two rounds of scan then pending per chain despite errors, got %v", got)
	}
	for i, step := range got[:8] {
		if step != want[i%4] {
			t.Fatalf("step %d is %q, want %q (all: %v)", i, step, want[i%4], got)
		}
	}
}

type countingBalances struct{ runs atomic.Int32 }

func (b *countingBalances) RefreshAll(context.Context) (refresh.PassSummary, error) {
	b.runs.Add(1)
	return refresh.PassSummary{Refreshed: 1}, nil
}

func TestStart_Refreshes_BalancesOnItsInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	balances := &countingBalances{}
	cfg := Config{ConfirmationInterval: time.Hour, BalanceRefreshInterval: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Balances: balances}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(MinInterval / 2)
	if balances.runs.Load() != 0 {
		t.Fatal("the balance refresh must wait one interval")
	}
	deadline := time.Now().Add(3 * MinInterval)
	for time.Now().Before(deadline) && balances.runs.Load() < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	if balances.runs.Load() < 2 {
		t.Fatalf("expected repeated balance refreshes, got %d", balances.runs.Load())
	}
}

func TestStart_Rejects_InvalidBalanceRefreshConfiguration(t *testing.T) {
	checker := &countingChecker{}
	cases := map[string]struct {
		interval time.Duration
		balances BalanceRefresher
	}{
		"interval too short": {time.Millisecond, &countingBalances{}},
		"missing refresher":  {MinInterval, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Config{ConfirmationInterval: MinInterval, BalanceRefreshInterval: tc.interval}
			if _, err := Start(context.Background(), cfg, Workers{Checker: checker, Balances: tc.balances}); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestParse_Chain_List(t *testing.T) {
	cases := map[string][]string{
		"":              nil,
		" , ,":          nil,
		"sol":           {"sol"},
		" sol , tsol ,": {"sol", "tsol"},
	}
	for raw, want := range cases {
		got := ParseChainList(raw)
		if len(got) != len(want) {
			t.Fatalf("%q: got %v, want %v", raw, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: got %v, want %v", raw, got, want)
			}
		}
	}
}

// stuckScanner never finishes a scan of stuckChain until the test ends.
type stuckScanner struct {
	*recordingScanner
	stuckChain string
	release    chan struct{}
}

func (s *stuckScanner) ScanLatestBlocks(ctx context.Context, chainID string) error {
	err := s.recordingScanner.ScanLatestBlocks(ctx, chainID)
	if chainID == s.stuckChain {
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	}
	return err
}

func (s *recordingScanner) scansOf(chainID string) int {
	count := 0
	for _, scanned := range s.scanned() {
		if scanned == chainID {
			count++
		}
	}
	return count
}

func TestStart_Scans_FastChainsInLoopsOfTheirOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &stuckScanner{recordingScanner: &recordingScanner{}, stuckChain: "sol", release: make(chan struct{})}
	defer close(scanner.release)
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"sol", "eth", "arbitrum", "bsc"}, DepositScanInterval: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * MinInterval)
	for time.Now().Before(deadline) && (scanner.scansOf("arbitrum") < 2 || scanner.scansOf("bsc") < 2) {
		time.Sleep(50 * time.Millisecond)
	}
	if scanner.scansOf("arbitrum") < 2 || scanner.scansOf("bsc") < 2 {
		t.Fatalf("arbitrum and bsc must keep scanning while sol is stuck, got %v", scanner.scanned())
	}
	if got := scanner.scansOf("sol"); got != 1 {
		t.Fatalf("sol must be scanned once and stay stuck, got %d", got)
	}
	if got := scanner.scansOf("eth"); got != 0 {
		t.Fatalf("eth shares the loop stuck on sol, got %d scans", got)
	}
}

// cancelAwareChecker blocks each run until ctx is cancelled, then takes finishDelay
// to wind down, like an RPC call returning after its context ended.
type cancelAwareChecker struct {
	started     chan struct{}
	startOnce   sync.Once
	finishDelay time.Duration
	finished    atomic.Bool
}

func (c *cancelAwareChecker) RunWithdrawalConfirmationCheck(ctx context.Context) error {
	c.startOnce.Do(func() { close(c.started) })
	<-ctx.Done()
	time.Sleep(c.finishDelay)
	c.finished.Store(true)
	return ctx.Err()
}

func TestLoops_Wait_ReturnsAfterCancelOnceTheRunInProgressFinished(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker := &cancelAwareChecker{started: make(chan struct{}), finishDelay: 200 * time.Millisecond}
	cfg := Config{
		ConfirmationInterval:   MinInterval,
		DeliveryInterval:       MinInterval,
		DeliverOutbox:          true,
		DepositScanChains:      []string{"sol", "base"},
		DepositScanInterval:    MinInterval,
		BalanceRefreshInterval: MinInterval,
	}
	workers := Workers{Checker: checker, Deliverer: &countingDeliverer{}, Scanner: &recordingScanner{}, Balances: &countingBalances{}}

	loops, err := Start(ctx, cfg, workers)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-checker.started:
	case <-time.After(3 * MinInterval):
		t.Fatal("the confirmation loop never ran")
	}

	cancel()
	waited := make(chan struct{})
	go func() {
		loops.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after ctx was cancelled")
	}
	if !checker.finished.Load() {
		t.Fatal("Wait must not return before the run in progress finished")
	}
}

func TestLoops_Wait_ReturnsRightAwayForIdleLoops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	loops, err := Start(ctx, Config{ConfirmationInterval: time.Hour}, Workers{Checker: &countingChecker{}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	waited := make(chan struct{})
	go func() {
		loops.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("idle loops must stop as soon as ctx is cancelled")
	}
}

func TestLoops_Wait_OnNilLoopsReturns(t *testing.T) {
	var loops *Loops
	loops.Wait()
}

func TestSplit_Scan_ChainsKeepsTheConfiguredOrder(t *testing.T) {
	shared, dedicated := splitScanChains([]string{"sol", "base", "eth", "btc", "tarbitrum", "polygon", "bsc"})

	if want := []string{"sol", "eth", "btc", "polygon"}; strings.Join(shared, ",") != strings.Join(want, ",") {
		t.Fatalf("shared %v, want %v", shared, want)
	}
	if want := []string{"base", "tarbitrum", "bsc"}; strings.Join(dedicated, ",") != strings.Join(want, ",") {
		t.Fatalf("dedicated %v, want %v", dedicated, want)
	}
}
