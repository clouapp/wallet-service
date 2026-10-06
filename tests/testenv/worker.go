package testenv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Parallel integration runs (make test-integration) give every test binary its own
// PostgreSQL database, cloned from a template migrated once, and its own Redis index.
const (
	// TemplateVariable names the migrated template database; set, it turns worker mode on.
	TemplateVariable = "TEST_DB_TEMPLATE"
	// WorkersVariable is the number of worker slots (go test -p).
	WorkersVariable = "TEST_DB_WORKERS"
	// LockDirVariable overrides the directory of the worker slot locks.
	LockDirVariable = "TEST_DB_LOCK_DIR"

	// MaxWorkers keeps the worker Redis indexes (REDIS_DB - slot) inside 9..14 and,
	// with workerMaxOpenConns, at most 60 connections on the PostgreSQL server the
	// e2e API shares (max_connections 100).
	MaxWorkers = 6
	// workerMaxOpenConns caps each worker's pool (DB_MAX_OPEN_CONNS, default 100).
	workerMaxOpenConns = 10

	// workerDatabaseInfix joins the template name and the slot: vault_unit_test_p3.
	workerDatabaseInfix  = "_p"
	defaultLockDir       = ".local/state/macro-e2e/locks"
	workerLockPrefix     = DefaultTestDatabaseName + ".worker-"
	maintenanceDatabase  = "postgres"
	maxOpenConnsVariable = "DB_MAX_OPEN_CONNS"

	workerSlotWait      = 15 * time.Minute
	workerSlotRetry     = 250 * time.Millisecond
	cloneAttempts       = 20
	cloneRetryDelay     = 250 * time.Millisecond
	maintenanceDeadline = 2 * time.Minute

	// pgObjectInUse is "source database is being accessed by other users".
	pgObjectInUse = "55006"
)

var (
	workerOnce sync.Once
	workerErr  error
	// workerLock stays open for the life of the test binary; the kernel drops the
	// flock when the process exits, however it exits.
	workerLock *os.File
)

// WorkerDatabaseName is the database of worker slot of template.
func WorkerDatabaseName(template string, slot int) string {
	return template + workerDatabaseInfix + strconv.Itoa(slot)
}

// IsWorkerDatabase reports whether name is a worker clone of template.
func IsWorkerDatabase(template, name string) bool {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(template+workerDatabaseInfix) + `[1-9][0-9]*$`).MatchString(name)
}

// WorkerMode reports whether this binary runs on a cloned worker database.
func WorkerMode() bool { return strings.TrimSpace(os.Getenv(TemplateVariable)) != "" }

// ValidateTemplate accepts only a dedicated test database as template.
func ValidateTemplate(template string) error {
	if err := ValidateConfiguration(Configuration{AppEnvironment: os.Getenv("APP_ENV"), DatabaseName: template}); err != nil {
		return err
	}
	if IsWorkerDatabase(DefaultTestDatabaseName, template) {
		return fmt.Errorf("refusing test setup: template %q is itself a worker clone", template)
	}
	return nil
}

// acquireWorker takes a free slot, recreates its database from the template and
// points DB_DATABASE and REDIS_DB at the slot. A no-op outside worker mode.
func acquireWorker() error {
	if !WorkerMode() {
		return nil
	}
	workerOnce.Do(func() { workerErr = setUpWorker() })
	return workerErr
}

func setUpWorker() error {
	template := strings.TrimSpace(os.Getenv(TemplateVariable))
	if err := ValidateTemplate(template); err != nil {
		return err
	}
	workers, err := workerCount()
	if err != nil {
		return err
	}
	baseRedis, err := strconv.Atoi(strings.TrimSpace(os.Getenv(redisDatabaseVariable)))
	if err != nil {
		return fmt.Errorf("refusing test setup: %s must be set before worker mode: %w", redisDatabaseVariable, err)
	}
	slot, err := lockWorkerSlot(workers)
	if err != nil {
		return err
	}
	database := WorkerDatabaseName(template, slot)
	if err := ValidateConfiguration(Configuration{AppEnvironment: os.Getenv("APP_ENV"), DatabaseName: database}); err != nil {
		return err
	}
	redisIndex := baseRedis - slot
	if redisIndex <= liveRedisDatabase {
		return fmt.Errorf("refusing test setup: worker %d would use Redis index %d", slot, redisIndex)
	}
	if err := cloneTemplate(template, database); err != nil {
		return err
	}
	os.Setenv(databaseVariable, database)
	os.Setenv(redisDatabaseVariable, strconv.Itoa(redisIndex))
	os.Setenv(maxOpenConnsVariable, strconv.Itoa(workerMaxOpenConns))
	return nil
}

func workerCount() (int, error) {
	raw := strings.TrimSpace(os.Getenv(WorkersVariable))
	if raw == "" {
		return 1, nil
	}
	workers, err := strconv.Atoi(raw)
	if err != nil || workers < 1 || workers > MaxWorkers {
		return 0, fmt.Errorf("refusing test setup: %s must be 1..%d, got %q", WorkersVariable, MaxWorkers, raw)
	}
	return workers, nil
}

func lockDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(LockDirVariable)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve worker lock directory: %w", err)
	}
	return filepath.Join(home, defaultLockDir), nil
}

// lockWorkerSlot holds an exclusive flock on the first free slot file, waiting for
// one when all are taken.
func lockWorkerSlot(workers int) (int, error) {
	dir, err := lockDir()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("create worker lock directory: %w", err)
	}
	deadline := time.Now().Add(workerSlotWait)
	for {
		for slot := 1; slot <= workers; slot++ {
			path := filepath.Join(dir, workerLockPrefix+strconv.Itoa(slot)+".lock")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				return 0, fmt.Errorf("open worker lock: %w", err)
			}
			if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
				workerLock = file
				return slot, nil
			}
			_ = file.Close()
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no free test worker slot of %d after %s", workers, workerSlotWait)
		}
		time.Sleep(workerSlotRetry)
	}
}

// maintenanceConnect opens the server's maintenance database with the DB_* settings.
func maintenanceConnect(ctx context.Context) (*pgx.Conn, error) {
	port := strings.TrimSpace(os.Getenv("DB_PORT"))
	if port == "" {
		port = "5432"
	}
	host := strings.TrimSpace(os.Getenv("DB_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	sslmode := strings.TrimSpace(os.Getenv("DB_SSLMODE"))
	if sslmode == "" {
		sslmode = "disable"
	}
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD")),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + maintenanceDatabase,
		RawQuery: "sslmode=" + url.QueryEscape(sslmode),
	}
	conn, err := pgx.Connect(ctx, dsn.String())
	if err != nil {
		return nil, fmt.Errorf("connect to the %s database at %s: %w", maintenanceDatabase, net.JoinHostPort(host, port), err)
	}
	return conn, nil
}

func cloneTemplate(template, database string) error {
	ctx, cancel := context.WithTimeout(context.Background(), maintenanceDeadline)
	defer cancel()
	conn, err := maintenanceConnect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop worker database %s: %w", database, err)
	}
	create := "CREATE DATABASE " + pgx.Identifier{database}.Sanitize() + " TEMPLATE " + pgx.Identifier{template}.Sanitize()
	for attempt := 1; ; attempt++ {
		_, err = conn.Exec(ctx, create)
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != pgObjectInUse || attempt >= cloneAttempts {
			break
		}
		time.Sleep(cloneRetryDelay)
	}
	if err != nil {
		return fmt.Errorf("clone %s into %s (run make test-integration, which migrates the template first): %w", template, database, err)
	}
	return nil
}

// DropWorkerDatabases drops every worker clone of template and returns their names.
func DropWorkerDatabases(template string) ([]string, error) {
	if err := ValidateTemplate(template); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), maintenanceDeadline)
	defer cancel()
	conn, err := maintenanceConnect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE starts_with(datname, $1)", template+workerDatabaseInfix)
	if err != nil {
		return nil, fmt.Errorf("list worker databases: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list worker databases: %w", err)
	}
	var dropped []string
	for _, name := range names {
		if !IsWorkerDatabase(template, name) || isProtectedDatabase(name) {
			continue
		}
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			return dropped, fmt.Errorf("drop worker database %s: %w", name, err)
		}
		dropped = append(dropped, name)
	}
	return dropped, nil
}
