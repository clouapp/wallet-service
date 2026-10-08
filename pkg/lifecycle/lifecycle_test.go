package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	testShutdownTimeout = 5 * time.Second
	shortTimeout        = 200 * time.Millisecond
	// promptly bounds a shutdown nothing blocks; far below testShutdownTimeout.
	promptly = time.Second
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeServer serves until Shutdown is called; shutdownBlock, when set, decides how
// long Shutdown takes after it stopped serving.
type fakeServer struct {
	serving       chan struct{}
	servingOnce   sync.Once
	stopped       chan struct{}
	stopOnce      sync.Once
	serveFailure  chan error
	shutdownBlock func(ctx context.Context) error
	shutdownCalls atomic.Int32

	mu          sync.Mutex
	deadline    time.Time
	hasDeadline bool
}

func newFakeServer() *fakeServer {
	return &fakeServer{serving: make(chan struct{}), stopped: make(chan struct{}), serveFailure: make(chan error, 1)}
}

func (s *fakeServer) Serve() error {
	s.servingOnce.Do(func() { close(s.serving) })
	select {
	case <-s.stopped:
		return nil
	case err := <-s.serveFailure:
		return err
	}
}

func (s *fakeServer) Shutdown(ctx context.Context) error {
	s.shutdownCalls.Add(1)
	s.mu.Lock()
	s.deadline, s.hasDeadline = ctx.Deadline()
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stopped) })
	if s.shutdownBlock != nil {
		return s.shutdownBlock(ctx)
	}
	return nil
}

func (s *fakeServer) shutdownDeadline() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline, s.hasDeadline
}

// fakeWorkers stop once their context is cancelled, unless hang is set: then they
// run until the test ends.
type fakeWorkers struct {
	hang    chan struct{}
	ctx     atomic.Pointer[context.Context]
	stopped chan struct{}
}

func newFakeWorkers(t *testing.T, hang bool) *fakeWorkers {
	w := &fakeWorkers{stopped: make(chan struct{})}
	if hang {
		w.hang = make(chan struct{})
		t.Cleanup(func() { close(w.hang) })
	}
	return w
}

func (w *fakeWorkers) start(ctx context.Context) Workers {
	w.ctx.Store(&ctx)
	go func() {
		defer close(w.stopped)
		<-ctx.Done()
		if w.hang != nil {
			<-w.hang
		}
	}()
	return w
}

func (w *fakeWorkers) Wait() { <-w.stopped }

func (w *fakeWorkers) contextCancelled() bool {
	ctx := w.ctx.Load()
	return ctx != nil && (*ctx).Err() != nil
}

type runResult struct {
	err     error
	elapsed time.Duration
}

// runInBackground starts Run and returns once the server is serving.
func runInBackground(t *testing.T, ctx context.Context, cfg Config, server *fakeServer) <-chan runResult {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quietLogger
	}
	results := make(chan runResult, 1)
	go func() {
		err := Run(ctx, cfg)
		results <- runResult{err: err}
	}()
	select {
	case <-server.serving:
	case result := <-results:
		t.Fatalf("Run returned before serving: %v", result.err)
	case <-time.After(promptly):
		t.Fatal("server never started serving")
	}
	return results
}

func awaitRun(t *testing.T, results <-chan runResult, since time.Time, limit time.Duration) runResult {
	t.Helper()
	select {
	case result := <-results:
		result.elapsed = time.Since(since)
		return result
	case <-time.After(limit):
		t.Fatalf("Run did not return within %s", limit)
		return runResult{}
	}
}

func TestRun_Signal_ShutsDownServerAndWorkersCleanly(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			server := newFakeServer()
			workers := newFakeWorkers(t, false)
			signals := make(chan os.Signal, 2)
			results := runInBackground(t, context.Background(), Config{
				Server: server, StartWorkers: workers.start, ShutdownTimeout: testShutdownTimeout, Signals: signals,
			}, server)

			if workers.contextCancelled() {
				t.Fatal("workers context cancelled before the signal")
			}
			sentAt := time.Now()
			signals <- sig
			result := awaitRun(t, results, sentAt, promptly)

			if result.err != nil {
				t.Fatalf("clean shutdown must return nil, got %v", result.err)
			}
			if calls := server.shutdownCalls.Load(); calls != 1 {
				t.Fatalf("server Shutdown called %d times, want 1", calls)
			}
			deadline, ok := server.shutdownDeadline()
			if !ok {
				t.Fatal("server Shutdown context carries no deadline")
			}
			if until := deadline.Sub(sentAt); until <= testShutdownTimeout-promptly || until > testShutdownTimeout+promptly {
				t.Fatalf("server Shutdown deadline %s after the signal, want about %s", until, testShutdownTimeout)
			}
			if !workers.contextCancelled() {
				t.Fatal("workers context must be cancelled on shutdown")
			}
		})
	}
}

func TestRun_Cancelled_ContextShutsDownCleanly(t *testing.T) {
	server := newFakeServer()
	workers := newFakeWorkers(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := runInBackground(t, ctx, Config{
		Server: server, StartWorkers: workers.start, ShutdownTimeout: testShutdownTimeout, Signals: make(chan os.Signal),
	}, server)

	cancelledAt := time.Now()
	cancel()
	result := awaitRun(t, results, cancelledAt, promptly)

	if result.err != nil {
		t.Fatalf("clean shutdown must return nil, got %v", result.err)
	}
	if server.shutdownCalls.Load() != 1 || !workers.contextCancelled() {
		t.Fatalf("expected server shutdown and cancelled workers, got shutdown calls=%d cancelled=%v", server.shutdownCalls.Load(), workers.contextCancelled())
	}
}

func TestRun_Times_Out(t *testing.T) {
	cases := map[string]struct {
		shutdownBlock func(t *testing.T) func(ctx context.Context) error
		hangWorkers   bool
		stillRunning  string
	}{
		"server ignoring its deadline": {
			shutdownBlock: func(t *testing.T) func(context.Context) error {
				release := make(chan struct{})
				t.Cleanup(func() { close(release) })
				return func(context.Context) error { <-release; return nil }
			},
			stillRunning: componentServer,
		},
		"server draining until its deadline": {
			shutdownBlock: func(*testing.T) func(context.Context) error {
				return func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			},
			stillRunning: componentServer,
		},
		"hanging workers": {
			hangWorkers:  true,
			stillRunning: componentWorkers,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := newFakeServer()
			if tc.shutdownBlock != nil {
				server.shutdownBlock = tc.shutdownBlock(t)
			}
			workers := newFakeWorkers(t, tc.hangWorkers)
			signals := make(chan os.Signal, 2)
			results := runInBackground(t, context.Background(), Config{
				Server: server, StartWorkers: workers.start, ShutdownTimeout: shortTimeout, Signals: signals,
			}, server)

			sentAt := time.Now()
			signals <- syscall.SIGTERM
			result := awaitRun(t, results, sentAt, shortTimeout+promptly)

			if !errors.Is(result.err, ErrShutdownTimeout) {
				t.Fatalf("expected ErrShutdownTimeout, got %v", result.err)
			}
			if result.elapsed < shortTimeout {
				t.Fatalf("Run returned after %s, before the %s deadline", result.elapsed, shortTimeout)
			}
			if got := result.err.Error(); !strings.HasSuffix(got, "still running: "+tc.stillRunning) {
				t.Fatalf("error must name only %q as still running, got %q", tc.stillRunning, got)
			}
			if !workers.contextCancelled() {
				t.Fatal("workers context must be cancelled even when the shutdown times out")
			}
		})
	}
}

func TestRun_Second_SignalAbandonsTheShutdown(t *testing.T) {
	server := newFakeServer()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	server.shutdownBlock = func(context.Context) error { <-release; return nil }
	signals := make(chan os.Signal, 2)
	results := runInBackground(t, context.Background(), Config{
		Server: server, ShutdownTimeout: testShutdownTimeout, Signals: signals,
	}, server)

	sentAt := time.Now()
	signals <- syscall.SIGTERM
	signals <- syscall.SIGINT
	result := awaitRun(t, results, sentAt, promptly)

	if !errors.Is(result.err, ErrForcedShutdown) {
		t.Fatalf("expected ErrForcedShutdown, got %v", result.err)
	}
}

func TestRun_Server_FailureStopsWorkersAndFails(t *testing.T) {
	server := newFakeServer()
	workers := newFakeWorkers(t, false)
	results := runInBackground(t, context.Background(), Config{
		Server: server, StartWorkers: workers.start, ShutdownTimeout: testShutdownTimeout, Signals: make(chan os.Signal),
	}, server)

	failedAt := time.Now()
	server.serveFailure <- errors.New("accept: too many open files")
	result := awaitRun(t, results, failedAt, promptly)

	if !errors.Is(result.err, ErrServerStopped) || !strings.Contains(result.err.Error(), "too many open files") {
		t.Fatalf("expected ErrServerStopped carrying the serve error, got %v", result.err)
	}
	if server.shutdownCalls.Load() != 1 || !workers.contextCancelled() {
		t.Fatalf("expected server shutdown and cancelled workers, got shutdown calls=%d cancelled=%v", server.shutdownCalls.Load(), workers.contextCancelled())
	}
}

func TestRun_Reports_ServerShutdownError(t *testing.T) {
	server := newFakeServer()
	server.shutdownBlock = func(context.Context) error { return errors.New("connection reset") }
	signals := make(chan os.Signal, 1)
	results := runInBackground(t, context.Background(), Config{
		Server: server, ShutdownTimeout: testShutdownTimeout, Signals: signals,
	}, server)

	sentAt := time.Now()
	signals <- syscall.SIGTERM
	result := awaitRun(t, results, sentAt, promptly)

	if result.err == nil || errors.Is(result.err, ErrShutdownTimeout) || !strings.Contains(result.err.Error(), "connection reset") {
		t.Fatalf("expected the server shutdown error, got %v", result.err)
	}
}

func TestRun_Without_Workers(t *testing.T) {
	cases := map[string]StartWorkers{
		"no starter":            nil,
		"starter starts no one": func(context.Context) Workers { return nil },
	}
	for name, start := range cases {
		t.Run(name, func(t *testing.T) {
			server := newFakeServer()
			signals := make(chan os.Signal, 1)
			results := runInBackground(t, context.Background(), Config{
				Server: server, StartWorkers: start, ShutdownTimeout: testShutdownTimeout, Signals: signals,
			}, server)

			sentAt := time.Now()
			signals <- syscall.SIGTERM
			if result := awaitRun(t, results, sentAt, promptly); result.err != nil {
				t.Fatalf("clean shutdown must return nil, got %v", result.err)
			}
		})
	}
}

func TestRun_Rejects_InvalidConfiguration(t *testing.T) {
	var missingCtx context.Context
	cases := map[string]struct {
		ctx context.Context
		cfg Config
	}{
		"missing context":  {missingCtx, Config{Server: newFakeServer(), ShutdownTimeout: time.Second}},
		"missing server":   {context.Background(), Config{ShutdownTimeout: time.Second}},
		"zero timeout":     {context.Background(), Config{Server: newFakeServer()}},
		"negative timeout": {context.Background(), Config{Server: newFakeServer(), ShutdownTimeout: -time.Second}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			started := false
			tc.cfg.StartWorkers = func(context.Context) Workers { started = true; return nil }
			if err := Run(tc.ctx, tc.cfg); err == nil {
				t.Fatal("expected a configuration error")
			}
			if started {
				t.Fatal("workers must not start with an invalid configuration")
			}
		})
	}
}
