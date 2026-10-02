package localworkers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestStart_RejectsInvalidConfiguration(t *testing.T) {
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
			if err := Start(context.Background(), tc.cfg, tc.checker, tc.deliverer, nil); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestStart_WaitsOneIntervalThenRunsBothLoops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker := &countingChecker{}
	deliverer := &countingDeliverer{}
	cfg := Config{ConfirmationInterval: MinInterval, DeliveryInterval: MinInterval, DeliverOutbox: true}

	if err := Start(ctx, cfg, checker, deliverer, nil); err != nil {
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

func TestStart_SkipsOutboxWhenAQueueDelivers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker := &countingChecker{}
	deliverer := &countingDeliverer{}

	if err := Start(ctx, Config{ConfirmationInterval: MinInterval}, checker, deliverer, nil); err != nil {
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
	mu      sync.Mutex
	chains  []string
	synced  []string
	err     error
	syncErr error
}

func (s *recordingScanner) ScanLatestBlocks(_ context.Context, chainID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains = append(s.chains, chainID)
	return s.err
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

func TestStart_SyncsAddressCachesBeforeTheFirstScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{syncErr: errors.New("redis down")}
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"sol", "eth", "btc"}, DepositScanInterval: MinInterval}

	if err := Start(ctx, cfg, &countingChecker{}, nil, scanner); err != nil {
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

func TestStart_RejectsInvalidDepositScanConfiguration(t *testing.T) {
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
			if err := Start(context.Background(), cfg, checker, nil, tc.scanner); err == nil {
				t.Fatal("expected a configuration error")
			}
		})
	}
}

func TestStart_ScansEveryConfiguredChainAndKeepsGoingAfterErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{err: errors.New("rate limited")}
	chains := []string{"sol", "tsol"}
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: chains, DepositScanInterval: MinInterval}

	if err := Start(ctx, cfg, &countingChecker{}, nil, scanner); err != nil {
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

func TestStart_NoScanWithoutChains(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{}
	if err := Start(ctx, Config{ConfirmationInterval: MinInterval, DepositScanInterval: MinInterval}, &countingChecker{}, nil, scanner); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(MinInterval + MinInterval/2)
	if got := scanner.scanned(); len(got) != 0 {
		t.Fatalf("no chain configured, got scans %v", got)
	}
}

func TestParseChainList(t *testing.T) {
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
