package testenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	testingEnvironmentName = "testing"
	databaseVariable       = "DB_DATABASE"
	redisDatabaseVariable  = "REDIS_DB"
	liveRedisDatabase      = 0
	maxRedisDatabase       = 15
)

const (
	// DefaultTestDatabaseName is the dedicated database the suite migrates fresh.
	DefaultTestDatabaseName = "vault_unit_test"
	// DevelopmentDatabaseName is the local dev database (.env.dev).
	DevelopmentDatabaseName = "vault"
	// E2EDatabaseName serves the local e2e API; it holds MPC shares that
	// cannot be recreated, so a wipe strands on-chain funds.
	E2EDatabaseName = "vault_test"
)

// protectedDatabaseNames are live databases the destructive setup never touches;
// every other target must also start with DefaultTestDatabaseName.
var protectedDatabaseNames = [...]string{DevelopmentDatabaseName, E2EDatabaseName}

// DatabaseOverrideVariable points the destructive test setup at another
// vault_unit_test* database so the suite can run while vault_test serves a live app.
const DatabaseOverrideVariable = "TEST_DB_DATABASE"

type Configuration struct {
	AppEnvironment string
	DatabaseName   string
}

func Load() error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}

	developmentEnvironmentPath := filepath.Join(root, ".env.dev")
	if err := godotenv.Load(developmentEnvironmentPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load %s: %w", developmentEnvironmentPath, err)
		}
		developmentEnvironmentExamplePath := filepath.Join(root, ".env.dev.example")
		if err := godotenv.Load(developmentEnvironmentExamplePath); err != nil {
			return fmt.Errorf("load fallback %s: %w", developmentEnvironmentExamplePath, err)
		}
	}

	testingEnvironmentPath := filepath.Join(root, ".env.testing")
	if err := godotenv.Overload(testingEnvironmentPath); err != nil {
		return fmt.Errorf("load required testing environment %s: %w", testingEnvironmentPath, err)
	}
	applyDatabaseOverride()
	if err := acquireWorker(); err != nil {
		return err
	}
	if err := isolateRedis(); err != nil {
		return err
	}

	return ValidateConfiguration(CurrentConfiguration())
}

// isolateRedis refuses a REDIS_DB that is the live index. The app reads its Redis
// connection from REDIS_HOST/PORT/PASSWORD/DB (config/database.go), so the index
// .env.testing sets here is the one the suite uses; without it the suite would share
// DB 0 with a running dev/e2e API and overwrite its caches.
func isolateRedis() error {
	return ValidateRedisDatabase(os.Getenv(redisDatabaseVariable))
}

// ValidateRedisDatabase accepts only a non-zero Redis index up to maxRedisDatabase.
func ValidateRedisDatabase(testDatabase string) error {
	index, err := strconv.Atoi(strings.TrimSpace(testDatabase))
	if err != nil || index <= liveRedisDatabase || index > maxRedisDatabase {
		return fmt.Errorf(
			"refusing test setup: %s must be a Redis index between %d and %d, got %q (index %d is the live dev/e2e one)",
			redisDatabaseVariable, liveRedisDatabase+1, maxRedisDatabase, testDatabase, liveRedisDatabase,
		)
	}
	return nil
}

func applyDatabaseOverride() {
	override := strings.TrimSpace(os.Getenv(DatabaseOverrideVariable))
	if override == "" {
		return
	}
	os.Setenv(databaseVariable, override)
}

func CurrentConfiguration() Configuration {
	return Configuration{
		AppEnvironment: strings.TrimSpace(os.Getenv("APP_ENV")),
		DatabaseName:   strings.TrimSpace(os.Getenv(databaseVariable)),
	}
}

func ValidateConfiguration(configuration Configuration) error {
	if strings.TrimSpace(configuration.AppEnvironment) != testingEnvironmentName {
		return fmt.Errorf("refusing destructive test setup: APP_ENV must be testing")
	}

	databaseName := strings.TrimSpace(configuration.DatabaseName)
	if databaseName == "" {
		return fmt.Errorf("refusing destructive test setup: database name is required")
	}
	if isProtectedDatabase(databaseName) {
		return fmt.Errorf(
			"refusing destructive test setup: database %q is protected (live dev/e2e data); set %s to a dedicated database starting with %s",
			databaseName,
			DatabaseOverrideVariable,
			DefaultTestDatabaseName,
		)
	}
	if !strings.HasPrefix(databaseName, DefaultTestDatabaseName) {
		return fmt.Errorf(
			"refusing destructive test setup: database %q must start with %s",
			databaseName,
			DefaultTestDatabaseName,
		)
	}
	return nil
}

func isProtectedDatabase(databaseName string) bool {
	for _, protected := range protectedDatabaseNames {
		if strings.EqualFold(databaseName, protected) {
			return true
		}
	}
	return false
}

func repositoryRoot() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve repository root: caller information is unavailable")
	}
	// environment.go lives in tests/feature/support/testenv, four levels under the module root.
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..")), nil
}
