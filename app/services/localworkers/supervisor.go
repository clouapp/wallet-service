package localworkers

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultSupervisorTick is how often the supervisor reads the scanned chains and
	// the deposit stream the settings ask for: a chain added to local_workers starts
	// scanning, and a removed one stops, within one tick, no restart.
	DefaultSupervisorTick = 15 * time.Second
	// streamStopWait bounds how long a replaced deposit stream is waited for before
	// the next one opens (a stream write waits at most 5 s).
	streamStopWait = 10 * time.Second
)

// ScanChainSource is the list of chains the local workers scan, read at the moment of
// use: the local_workers platform setting, else LOCAL_DEPOSIT_SCAN_CHAINS. A failed
// read keeps the chains being scanned.
type ScanChainSource interface {
	ScanChains(ctx context.Context) ([]string, error)
}

// DepositStreamSpec is the deposit stream the settings ask for. Key tells two specs
// apart (a digest of what the stream reads, never its URL); an empty Key is no stream.
type DepositStreamSpec struct {
	Key   string
	Chain string
}

// DepositStreams is the deposit stream the settings ask for now (xrp_stream, else
// LOCAL_XRP_STREAM_*), and how to open one.
type DepositStreams interface {
	StreamSpec(ctx context.Context) (DepositStreamSpec, error)
	OpenStream(ctx context.Context, spec DepositStreamSpec) (DepositStream, error)
}

// ChainSync re-applies, in a running process, what changed in the chain catalog since
// the registry was built (each chain's RPC endpoint list, the TronGrid key). Run
// returns when ctx is cancelled.
type ChainSync interface {
	Run(ctx context.Context)
}

// scanSupervisor owns the scanned chain list: the shared loop scans its shared
// chains on each tick, each dedicated chain has a loop of its own it starts and
// cancels, and the backfill and the signature schedule read the list it holds.
type scanSupervisor struct {
	ctx      context.Context
	loops    *Loops
	scanner  DepositScanner
	interval time.Duration
	known    func(chainID string) bool

	mu        sync.RWMutex
	chains    []string
	dedicated map[string]context.CancelFunc
	refused   map[string]bool
}

func newScanSupervisor(ctx context.Context, loops *Loops, scanner DepositScanner, interval time.Duration, known func(string) bool) *scanSupervisor {
	return &scanSupervisor{
		ctx: ctx, loops: loops, scanner: scanner, interval: interval, known: known,
		dedicated: map[string]context.CancelFunc{}, refused: map[string]bool{},
	}
}

// current is the scanned chains, in their configured order.
func (s *scanSupervisor) current() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.chains)
}

// shared is the scanned chains the shared loop reads, in order.
func (s *scanSupervisor) shared() []string {
	shared, _ := splitScanChains(s.current())
	return shared
}

// apply makes list the scanned chains: an added chain has its address cache synced
// and, when it is dedicated, a loop started; a removed dedicated chain has its loop
// cancelled (its cycle in progress ends first). A blank, repeated or unknown chain is
// skipped, an unknown one logged once.
func (s *scanSupervisor) apply(list []string) {
	next := s.normalize(list)
	s.mu.Lock()
	previous := s.chains
	s.chains = next
	var added, removed []string
	for _, chainID := range next {
		if !slices.Contains(previous, chainID) {
			added = append(added, chainID)
		}
	}
	for _, chainID := range previous {
		if !slices.Contains(next, chainID) {
			removed = append(removed, chainID)
		}
	}
	for _, chainID := range removed {
		if cancel, ok := s.dedicated[chainID]; ok {
			cancel()
			delete(s.dedicated, chainID)
		}
	}
	var start []string
	for _, chainID := range added {
		if dedicatedScanChains[chainID] {
			start = append(start, chainID)
		}
	}
	s.mu.Unlock()

	for _, chainID := range added {
		syncAddressCache(s.ctx, s.scanner, chainID)
	}
	for _, chainID := range start {
		s.startDedicated(chainID)
	}
	if previous != nil && (len(added) > 0 || len(removed) > 0) {
		slog.Info("local deposit scan chains changed",
			"chains", strings.Join(next, ","), "added", strings.Join(added, ","), "removed", strings.Join(removed, ","))
	}
}

func (s *scanSupervisor) startDedicated(chainID string) {
	loopCtx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if !slices.Contains(s.chains, chainID) {
		s.mu.Unlock()
		cancel()
		return
	}
	s.dedicated[chainID] = cancel
	s.mu.Unlock()
	s.loops.every(loopCtx, s.interval, func() {
		scanChain(loopCtx, s.scanner, chainID)
	})
}

func (s *scanSupervisor) normalize(list []string) []string {
	next := []string{}
	for _, raw := range list {
		chainID := strings.TrimSpace(raw)
		if chainID == "" || slices.Contains(next, chainID) {
			continue
		}
		if s.known != nil && !s.known(chainID) {
			s.mu.Lock()
			first := !s.refused[chainID]
			s.refused[chainID] = true
			s.mu.Unlock()
			if first {
				slog.Error("local deposit scan chain skipped: the chain registry does not hold it", "chain", chainID)
			}
			continue
		}
		next = append(next, chainID)
	}
	return next
}

// streamSupervisor runs at most one deposit stream: the one the settings ask for,
// when its chain is scanned. A changed spec stops the running stream and opens the
// new one; a failed read keeps the running one.
type streamSupervisor struct {
	streams DepositStreams
	loops   *Loops

	running string
	cancel  context.CancelFunc
	done    chan struct{}
	logged  string
}

func (s *streamSupervisor) reconcile(ctx context.Context, scanned []string) {
	if s == nil || s.streams == nil {
		return
	}
	spec, err := s.streams.StreamSpec(ctx)
	if err != nil {
		s.once("read:"+err.Error(), func() {
			slog.Warn("deposit stream settings unread; the running stream is kept", "error", err)
		})
		return
	}
	if spec.Key == "" {
		s.stop()
		return
	}
	if !slices.Contains(scanned, spec.Chain) {
		s.stop()
		s.once("unscanned:"+spec.Key, func() {
			slog.Error("deposit stream not started: its chain is not scanned (the ledger scan stays the guarantee)", "chain", spec.Chain)
		})
		return
	}
	if spec.Key == s.running {
		return
	}
	s.stop()
	stream, err := s.streams.OpenStream(ctx, spec)
	if err != nil {
		s.once("open:"+spec.Key, func() {
			slog.Error("deposit stream not started: enabled but not configured; the other local workers run", "chain", spec.Chain, "error", err)
		})
		return
	}
	streamCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.running, s.cancel, s.done, s.logged = spec.Key, cancel, done, ""
	s.loops.running.Go(func() {
		defer close(done)
		stream.Run(streamCtx)
	})
	slog.Info("deposit stream started", "chain", spec.Chain)
}

// stop cancels the running stream and waits for it, at most streamStopWait.
func (s *streamSupervisor) stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(streamStopWait):
		slog.Warn("deposit stream did not stop in time; the next one opens beside it")
	}
	s.running, s.cancel, s.done = "", nil, nil
	slog.Info("deposit stream stopped")
}

// once runs log the first time key is seen in a row.
func (s *streamSupervisor) once(key string, log func()) {
	if s.logged == key {
		return
	}
	s.logged = key
	log()
}
