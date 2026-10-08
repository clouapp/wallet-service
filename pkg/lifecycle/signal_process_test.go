package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The real-signal tests re-execute this test binary as a child process that runs
// Run (helperModeEnv selects how); the parent sends it a real signal and checks how
// it exits.
const (
	helperModeEnv      = "LIFECYCLE_SIGNAL_HELPER_MODE"
	helperTimeoutEnv   = "LIFECYCLE_SIGNAL_HELPER_TIMEOUT_MS"
	helperModeClean    = "clean"
	helperModeHang     = "hang-workers"
	helperModeNoRun    = "no-run"
	helperReadyLine    = "LIFECYCLE_HELPER_READY\n"
	helperExitTimeout  = 3
	helperExitFailure  = 1
	helperStartupLimit = 10 * time.Second
	helperExitLimit    = 10 * time.Second
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperModeEnv); mode != "" {
		os.Exit(runSignalHelper(mode))
	}
	os.Exit(m.Run())
}

// runSignalHelper is the child process. Like Goravel's foundation.Build, it first
// subscribes to SIGINT/SIGTERM without acting on them, which disables Go's default
// exit on those signals: only Run can make the process exit.
func runSignalHelper(mode string) int {
	_, stopGoravelLike := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopGoravelLike()

	if mode == helperModeNoRun {
		fmt.Print(helperReadyLine)
		time.Sleep(helperExitLimit)
		return helperExitFailure
	}

	timeoutMs, err := strconv.Atoi(os.Getenv(helperTimeoutEnv))
	if err != nil || timeoutMs <= 0 {
		fmt.Fprintf(os.Stderr, "invalid %s: %q\n", helperTimeoutEnv, os.Getenv(helperTimeoutEnv))
		return helperExitFailure
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return helperExitFailure
	}
	server, err := NewListenerServer(ListenerServerDeps{Router: &httpRouter{}, Listener: listener})
	if err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		return helperExitFailure
	}

	err = Run(context.Background(), Config{
		Server:          server,
		StartWorkers:    helperWorkers(mode == helperModeHang),
		ShutdownTimeout: time.Duration(timeoutMs) * time.Millisecond,
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	switch {
	case errors.Is(err, ErrShutdownTimeout):
		return helperExitTimeout
	case err != nil:
		fmt.Fprintln(os.Stderr, "run:", err)
		return helperExitFailure
	}
	return 0
}

type helperLoops struct{ running sync.WaitGroup }

func (l *helperLoops) Wait() { l.running.Wait() }

// helperWorkers runs a ticking loop like localworkers; it announces readiness once
// Run subscribed to the signals, since Run subscribes before starting the workers.
func helperWorkers(hang bool) StartWorkers {
	return func(ctx context.Context) Workers {
		loops := &helperLoops{}
		loops.running.Add(1)
		go func() {
			defer loops.running.Done()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					if hang {
						select {}
					}
					return
				case <-ticker.C:
				}
			}
		}()
		fmt.Print(helperReadyLine)
		return loops
	}
}

// readyWriter collects the child's stdout and closes ready at the readiness line.
type readyWriter struct {
	mu        sync.Mutex
	output    bytes.Buffer
	ready     chan struct{}
	readyOnce sync.Once
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.output.Write(p)
	if bytes.Contains(w.output.Bytes(), []byte(helperReadyLine)) {
		w.readyOnce.Do(func() { close(w.ready) })
	}
	return n, err
}

// helperProcess.exited is closed once the child exited; cmd.ProcessState and stderr
// are only read after that.
type helperProcess struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	exited chan struct{}
}

func startSignalHelper(t *testing.T, mode string, shutdownTimeout time.Duration) *helperProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		helperModeEnv+"="+mode,
		helperTimeoutEnv+"="+strconv.FormatInt(shutdownTimeout.Milliseconds(), 10),
	)
	stdout := &readyWriter{ready: make(chan struct{})}
	stderr := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	process := &helperProcess{cmd: cmd, stderr: stderr, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(process.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-process.exited:
		default:
			_ = cmd.Process.Kill()
			<-process.exited
		}
	})

	select {
	case <-stdout.ready:
	case <-process.exited:
		t.Fatalf("helper exited before it was ready (%s)\n%s", cmd.ProcessState, stderr.String())
	case <-time.After(helperStartupLimit):
		t.Fatal("helper never became ready")
	}
	return process
}

// signalAndWait sends sig and returns the exit code and how long the exit took.
func (p *helperProcess) signalAndWait(t *testing.T, sig os.Signal) (int, time.Duration) {
	t.Helper()
	sentAt := time.Now()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal helper: %v", err)
	}
	select {
	case <-p.exited:
		return p.cmd.ProcessState.ExitCode(), time.Since(sentAt)
	case <-time.After(helperExitLimit):
		t.Fatalf("helper ignored %s for %s", sig, helperExitLimit)
		return 0, 0
	}
}

func TestReal_Signal_ExitsZeroBeforeTheDeadline(t *testing.T) {
	const shutdownTimeout = 5 * time.Second
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			helper := startSignalHelper(t, helperModeClean, shutdownTimeout)

			code, elapsed := helper.signalAndWait(t, sig)

			if code != 0 {
				t.Fatalf("exit code %d, want 0\n%s", code, helper.stderr.String())
			}
			if elapsed >= shutdownTimeout {
				t.Fatalf("clean shutdown took %s, not below the %s deadline", elapsed, shutdownTimeout)
			}
		})
	}
}

func TestReal_Signal_HangingWorkersExitNonZeroAtTheDeadline(t *testing.T) {
	const shutdownTimeout = 300 * time.Millisecond
	helper := startSignalHelper(t, helperModeHang, shutdownTimeout)

	code, elapsed := helper.signalAndWait(t, syscall.SIGTERM)

	if code != helperExitTimeout {
		t.Fatalf("exit code %d, want %d\n%s", code, helperExitTimeout, helper.stderr.String())
	}
	if elapsed < shutdownTimeout {
		t.Fatalf("exited after %s, before the %s deadline", elapsed, shutdownTimeout)
	}
	if !bytes.Contains(helper.stderr.Bytes(), []byte("graceful shutdown deadline expired")) {
		t.Fatalf("missing the deadline log line:\n%s", helper.stderr.String())
	}
}

// TestRealSignal_IgnoredWithoutRun reproduces the original bug: with only the
// Goravel-like subscription, SIGTERM does not stop the process.
func TestReal_Signal_IgnoredWithoutRun(t *testing.T) {
	const observeFor = 500 * time.Millisecond
	helper := startSignalHelper(t, helperModeNoRun, time.Second)

	if err := helper.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal helper: %v", err)
	}
	select {
	case <-helper.exited:
		t.Fatalf("expected SIGTERM to be swallowed, but the helper exited (%s)", helper.cmd.ProcessState)
	case <-time.After(observeFor):
	}
}
