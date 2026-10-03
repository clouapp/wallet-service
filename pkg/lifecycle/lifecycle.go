// Package lifecycle runs a long-lived HTTP server and its background workers until
// SIGINT or SIGTERM, then stops both within a shutdown deadline.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrShutdownTimeout means the server or the workers were still running when the
	// shutdown deadline expired.
	ErrShutdownTimeout = errors.New("graceful shutdown deadline expired")
	// ErrForcedShutdown means a second signal arrived before the shutdown completed.
	ErrForcedShutdown = errors.New("shutdown forced by a second signal")
	// ErrServerStopped means the server stopped serving without being asked to.
	ErrServerStopped = errors.New("http server stopped unexpectedly")
)

const (
	componentServer  = "http server"
	componentWorkers = "background workers"
	// signalBuffer holds the first signal and the one that forces the shutdown.
	signalBuffer = 2
)

// Server is the HTTP server: Serve blocks while serving, Shutdown stops accepting
// connections and waits for the requests in flight until ctx expires.
type Server interface {
	Serve() error
	Shutdown(ctx context.Context) error
}

// Workers are background loops started with a context; Wait returns once every loop
// returned after that context was cancelled.
type Workers interface {
	Wait()
}

// StartWorkers launches the background workers bound to ctx. It returns nil when no
// worker was started.
type StartWorkers func(ctx context.Context) Workers

type Config struct {
	Server Server
	// StartWorkers is optional.
	StartWorkers StartWorkers
	// ShutdownTimeout bounds the whole shutdown: server drain and workers together.
	ShutdownTimeout time.Duration
	// Signals replaces the SIGINT/SIGTERM subscription; tests feed it directly.
	Signals <-chan os.Signal
	Logger  *slog.Logger
}

func (c Config) validate() error {
	if c.Server == nil {
		return errors.New("lifecycle: server is required")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("lifecycle: shutdown timeout must be positive, got %s", c.ShutdownTimeout)
	}
	return nil
}

// Run subscribes to SIGINT and SIGTERM, starts the workers, then serves until a
// signal arrives, ctx is cancelled or the server stops by itself. It then shuts the
// server down and cancels the workers' context at the same time, and waits for both
// up to ShutdownTimeout. It returns nil only after a clean shutdown that was asked for.
func Run(ctx context.Context, cfg Config) error {
	if ctx == nil {
		return errors.New("lifecycle: context is required")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	signals := cfg.Signals
	if signals == nil {
		subscription := make(chan os.Signal, signalBuffer)
		signal.Notify(subscription, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(subscription)
		signals = subscription
	}

	workersCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	var workers Workers
	if cfg.StartWorkers != nil {
		workers = cfg.StartWorkers(workersCtx)
	}

	served := make(chan error, 1)
	go func() { served <- cfg.Server.Serve() }()

	var serveErr error
	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String(), "timeout", cfg.ShutdownTimeout.String())
	case <-ctx.Done():
		logger.Info("shutdown requested", "reason", context.Cause(ctx).Error(), "timeout", cfg.ShutdownTimeout.String())
	case err := <-served:
		serveErr = errors.Join(ErrServerStopped, err)
		logger.Error("http server stopped, shutting down", "error", err)
	}

	shutdownErr := shutdown(cfg, logger, signals, cancelWorkers, workers)
	return errors.Join(serveErr, shutdownErr)
}

func shutdown(cfg Config, logger *slog.Logger, signals <-chan os.Signal, cancelWorkers context.CancelFunc, workers Workers) error {
	started := time.Now()
	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelDeadline()

	serverStopped := make(chan error, 1)
	go func() { serverStopped <- cfg.Server.Shutdown(deadlineCtx) }()
	cancelWorkers()
	workersStopped := make(chan struct{})
	go func() {
		if workers != nil {
			workers.Wait()
		}
		close(workersStopped)
	}()

	pending := map[string]bool{componentServer: true, componentWorkers: true}
	var serverErr error
	for len(pending) > 0 {
		select {
		case err := <-serverStopped:
			serverStopped = nil
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			delete(pending, componentServer)
			if err != nil {
				serverErr = fmt.Errorf("http server shutdown: %w", err)
			}
			logger.Info("http server stopped", "elapsed", time.Since(started).String())
		case <-workersStopped:
			workersStopped = nil
			delete(pending, componentWorkers)
			logger.Info("background workers stopped", "elapsed", time.Since(started).String())
		case <-deadlineCtx.Done():
			err := fmt.Errorf("%w after %s; still running: %s", ErrShutdownTimeout, cfg.ShutdownTimeout, pendingList(pending))
			logger.Error("graceful shutdown deadline expired", "timeout", cfg.ShutdownTimeout.String(), "still_running", pendingList(pending))
			return errors.Join(serverErr, err)
		case sig := <-signals:
			logger.Error("second signal received, abandoning the graceful shutdown", "signal", sig.String(), "still_running", pendingList(pending))
			return errors.Join(serverErr, fmt.Errorf("%w (%s); still running: %s", ErrForcedShutdown, sig, pendingList(pending)))
		}
	}
	if serverErr != nil {
		return serverErr
	}
	logger.Info("graceful shutdown complete", "elapsed", time.Since(started).String())
	return nil
}

func pendingList(pending map[string]bool) string {
	names := make([]string, 0, len(pending))
	for _, name := range []string{componentServer, componentWorkers} {
		if pending[name] {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}
