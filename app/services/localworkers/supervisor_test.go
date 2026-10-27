package localworkers

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// settableChains is the local_workers setting a test changes between ticks.
type settableChains struct {
	mu     sync.Mutex
	chains []string
	err    error
	reads  atomic.Int32
}

func (s *settableChains) set(chains ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains, s.err = chains, nil
}

func (s *settableChains) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *settableChains) ScanChains(context.Context) ([]string, error) {
	s.reads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return slices.Clone(s.chains), nil
}

func scannedSince(scanner *recordingScanner, from int) []string {
	all := scanner.scanned()
	if from > len(all) {
		return nil
	}
	return all[from:]
}

// TestStart_Scans_TheChainsTheSettingListsAndFollowsAChangeWithoutARestart: the
// first read decides what the first cycle scans (not LOCAL_DEPOSIT_SCAN_CHAINS), a
// chain added on a later tick has its cache synced and is scanned (a dedicated one
// in a loop of its own), and a chain removed stops being scanned.
func TestStart_Scans_TheChainsTheSettingListsAndFollowsAChangeWithoutARestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{progress: progressChan()}
	source := &settableChains{}
	source.set("eth")
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"btc"}, DepositScanInterval: MinInterval, SupervisorTick: MinInterval}

	loops, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner, ScanChains: source})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := scanner.cacheSyncs(); !slices.Equal(got, []string{"eth"}) {
		t.Fatalf("the first read must decide the scanned chains, synced %v", got)
	}
	waitUntil(t, 3*MinInterval, scanner.progress, func() bool { return slices.Contains(scanner.scanned(), "eth") })
	if got := scanner.scanned(); slices.Contains(got, "btc") || !slices.Contains(got, "eth") {
		t.Fatalf("scanned %v, want eth only", got)
	}

	source.set("eth", "sol")
	waitUntil(t, 5*MinInterval, scanner.progress, func() bool { return slices.Contains(scanner.scanned(), "sol") })
	if !slices.Contains(scanner.scanned(), "sol") {
		t.Fatalf("an added dedicated chain was never scanned: %v", scanner.scanned())
	}
	if !slices.Contains(scanner.cacheSyncs(), "sol") {
		t.Fatal("an added chain must have its address cache synced")
	}

	source.set("sol")
	// Wait for a supervisor tick to apply the removal, then for both loops to run again.
	reads := source.reads.Load()
	waitUntil(t, 5*MinInterval, scanner.progress, func() bool { return source.reads.Load() > reads })
	from := len(scanner.scanned())
	waitUntil(t, 4*MinInterval, scanner.progress, func() bool {
		return slices.Contains(scannedSince(scanner, from), "sol") && len(scannedSince(scanner, from)) >= 2
	})
	time.Sleep(2 * MinInterval)
	if got := scannedSince(scanner, from+1); slices.Contains(got, "eth") {
		t.Fatalf("a removed chain is still scanned: %v", got)
	}

	cancel()
	loops.Wait()
}

// TestStart_Keeps_TheScannedChainsWhenTheSettingCannotBeRead: a failed read at the
// start scans the configured list, and a failed read later keeps the running one.
func TestStart_Keeps_TheScannedChainsWhenTheSettingCannotBeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{progress: progressChan()}
	source := &settableChains{}
	source.fail(errors.New("db down"))
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"btc"}, DepositScanInterval: MinInterval, SupervisorTick: MinInterval}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner, ScanChains: source}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, 4*MinInterval, scanner.progress, func() bool { return len(scanner.scanned()) >= 2 })
	if got := scanner.scanned(); len(got) < 2 || slices.ContainsFunc(got, func(c string) bool { return c != "btc" }) {
		t.Fatalf("scanned %v, want the configured btc on every cycle", got)
	}
}

// TestStart_Skips_AScannedChainTheRegistryDoesNotHold: an unknown chain is logged
// and skipped; the others are scanned.
func TestStart_Skips_AScannedChainTheRegistryDoesNotHold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanner := &recordingScanner{progress: progressChan()}
	source := &settableChains{}
	source.set("nope", "eth", "eth")
	cfg := Config{
		ConfirmationInterval: time.Hour, DepositScanInterval: MinInterval,
		KnownChain: func(chainID string) bool { return chainID != "nope" },
	}

	if _, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: scanner, ScanChains: source}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := scanner.cacheSyncs(); !slices.Equal(got, []string{"eth"}) {
		t.Fatalf("synced %v, want eth once", got)
	}
}

// fakeStreams is the xrp_stream setting a test changes between ticks.
type fakeStreams struct {
	mu      sync.Mutex
	spec    DepositStreamSpec
	err     error
	opened  []string
	streams []*recordingStream
}

func (f *fakeStreams) set(spec DepositStreamSpec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spec, f.err = spec, nil
}

func (f *fakeStreams) StreamSpec(context.Context) (DepositStreamSpec, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spec, f.err
}

func (f *fakeStreams) OpenStream(_ context.Context, spec DepositStreamSpec) (DepositStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stream := &recordingStream{started: make(chan struct{})}
	f.opened = append(f.opened, spec.Key)
	f.streams = append(f.streams, stream)
	return stream, nil
}

func (f *fakeStreams) snapshot() ([]string, []*recordingStream) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opened), slices.Clone(f.streams)
}

// TestStart_Runs_TheDepositStreamTheSettingsAskForAndRestartsItOnAChange: a stream
// starts when its chain is scanned, a changed spec stops it and opens the new one, a
// chain the workers do not scan stops it, and no spec runs none.
func TestStart_Runs_TheDepositStreamTheSettingsAskForAndRestartsItOnAChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &settableChains{}
	source.set("txrp")
	streams := &fakeStreams{}
	streams.set(DepositStreamSpec{Key: "a", Chain: "txrp"})
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanInterval: time.Hour, SupervisorTick: MinInterval}

	loops, err := Start(ctx, cfg, Workers{Checker: &countingChecker{}, Scanner: &recordingScanner{}, ScanChains: source, Streams: streams})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	opened, running := streams.snapshot()
	if !slices.Equal(opened, []string{"a"}) {
		t.Fatalf("opened %v at start, want a", opened)
	}
	<-running[0].started

	streams.set(DepositStreamSpec{Key: "b", Chain: "txrp"})
	waitFor(t, 4*MinInterval, func() bool { o, _ := streams.snapshot(); return len(o) == 2 })
	opened, running = streams.snapshot()
	if !slices.Equal(opened, []string{"a", "b"}) || !running[0].stopped.Load() {
		t.Fatalf("a changed spec must stop the old stream and open the new: opened %v, old stopped %v", opened, running[0].stopped.Load())
	}

	source.set("xrp")
	waitFor(t, 4*MinInterval, func() bool { _, r := streams.snapshot(); return r[1].stopped.Load() })
	if _, running = streams.snapshot(); !running[1].stopped.Load() {
		t.Fatal("a stream whose chain is no longer scanned must stop")
	}

	source.set("txrp")
	streams.set(DepositStreamSpec{})
	time.Sleep(3 * MinInterval)
	if opened, _ = streams.snapshot(); len(opened) != 2 {
		t.Fatalf("no spec must open no stream, opened %v", opened)
	}

	cancel()
	loops.Wait()
}

func TestStart_Rejects_BothAConfiguredAndASettingsDepositStream(t *testing.T) {
	cfg := Config{ConfirmationInterval: time.Hour, DepositScanChains: []string{"txrp"}, DepositScanInterval: time.Hour, DepositStreamChain: "txrp"}
	workers := Workers{Checker: &countingChecker{}, Scanner: &recordingScanner{}, DepositStream: &recordingStream{started: make(chan struct{})}, Streams: &fakeStreams{}}
	if _, err := Start(context.Background(), cfg, workers); err == nil {
		t.Fatal("expected a configuration error")
	}
}

type countingSync struct{ runs atomic.Int32 }

func (c *countingSync) Run(ctx context.Context) {
	c.runs.Add(1)
	<-ctx.Done()
}

func TestStart_Runs_TheChainSyncUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sync := &countingSync{}
	loops, err := Start(ctx, Config{ConfirmationInterval: time.Hour}, Workers{Checker: &countingChecker{}, ChainSync: sync})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool { return sync.runs.Load() == 1 })
	cancel()
	loops.Wait()
}

func waitFor(t *testing.T, limit time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
