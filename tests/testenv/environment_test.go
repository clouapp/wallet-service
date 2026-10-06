package testenv

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

func TestValidateConfigurationAcceptsDedicatedTestDatabase(t *testing.T) {
	t.Parallel()

	err := ValidateConfiguration(Configuration{
		AppEnvironment: "testing",
		DatabaseName:   DefaultTestDatabaseName,
	})
	if err != nil {
		t.Fatalf("ValidateConfiguration() error = %v", err)
	}
}

func TestValidateConfigurationRejectsProtectedDatabases(t *testing.T) {
	t.Parallel()

	for _, databaseName := range []string{
		DevelopmentDatabaseName,
		E2EDatabaseName,
		" " + E2EDatabaseName + " ",
		strings.ToUpper(E2EDatabaseName),
	} {
		databaseName := databaseName
		t.Run(databaseName, func(t *testing.T) {
			t.Parallel()

			err := ValidateConfiguration(Configuration{AppEnvironment: "testing", DatabaseName: databaseName})
			if err == nil {
				t.Fatalf("ValidateConfiguration(%q) error = nil, want protected-database refusal", databaseName)
			}
			if !strings.Contains(err.Error(), "is protected") || !strings.Contains(err.Error(), DatabaseOverrideVariable) {
				t.Fatalf("ValidateConfiguration(%q) error = %q, want protected refusal naming %s", databaseName, err, DatabaseOverrideVariable)
			}
		})
	}
}

func TestTestingEnvironmentFileUsesDedicatedDatabase(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("repositoryRoot() error = %v", err)
	}
	values, err := godotenv.Read(filepath.Join(root, ".env.testing"))
	if err != nil {
		t.Fatalf("read .env.testing: %v", err)
	}
	if got := values[databaseVariable]; got != DefaultTestDatabaseName {
		t.Fatalf(".env.testing %s = %q, want %q", databaseVariable, got, DefaultTestDatabaseName)
	}
	if err := ValidateConfiguration(Configuration{AppEnvironment: values["APP_ENV"], DatabaseName: values[databaseVariable]}); err != nil {
		t.Fatalf(".env.testing defaults must pass the guard: %v", err)
	}
}

func TestApplyDatabaseOverrideToE2EDatabaseIsRefused(t *testing.T) {
	t.Setenv("APP_ENV", "testing")
	t.Setenv("DB_DATABASE", E2EDatabaseName)
	t.Setenv(DatabaseOverrideVariable, E2EDatabaseName)

	applyDatabaseOverride()

	err := ValidateConfiguration(CurrentConfiguration())
	if err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Fatalf("an override to %s must still be refused, got %v", E2EDatabaseName, err)
	}
}

func TestApplyDatabaseOverrideUsesIsolatedDatabase(t *testing.T) {
	t.Setenv("DB_DATABASE", "vault_test")
	t.Setenv(DatabaseOverrideVariable, " vault_accounts_test ")

	applyDatabaseOverride()

	if got := CurrentConfiguration().DatabaseName; got != "vault_accounts_test" {
		t.Fatalf("DatabaseName = %q, want vault_accounts_test", got)
	}
}

func TestApplyDatabaseOverrideKeepsDatabaseWhenUnset(t *testing.T) {
	t.Setenv("DB_DATABASE", "vault_test")
	t.Setenv(DatabaseOverrideVariable, "  ")

	applyDatabaseOverride()

	if got := CurrentConfiguration().DatabaseName; got != "vault_test" {
		t.Fatalf("DatabaseName = %q, want vault_test", got)
	}
}

func TestApplyDatabaseOverrideStillRejectsUnsafeDatabase(t *testing.T) {
	t.Setenv("APP_ENV", "testing")
	t.Setenv("DB_DATABASE", "vault_test")
	t.Setenv(DatabaseOverrideVariable, "vault")

	applyDatabaseOverride()

	err := ValidateConfiguration(CurrentConfiguration())
	if err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Fatalf("ValidateConfiguration() error = %v, want protected-database rejection", err)
	}
}

func TestTestRedisURLMovesTheDevURLToTheTestIndex(t *testing.T) {
	t.Parallel()

	testCases := []struct{ redisURL, want string }{
		{"redis://localhost:6380", "redis://localhost:6380/15"},
		{"redis://localhost:6380/0", "redis://localhost:6380/15"},
		{"redis://:secret@localhost:6380/3?dial_timeout=1s", "redis://:secret@localhost:6380/15?dial_timeout=1s"},
		{"", ""},
	}
	for _, testCase := range testCases {
		got, err := TestRedisURL(testCase.redisURL, "15")
		if err != nil {
			t.Fatalf("TestRedisURL(%q) error = %v", testCase.redisURL, err)
		}
		if got != testCase.want {
			t.Fatalf("TestRedisURL(%q) = %q, want %q", testCase.redisURL, got, testCase.want)
		}
	}
}

func TestTestRedisURLRefusesTheLiveIndexAndBadInput(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct{ redisURL, database string }{
		{"redis://localhost:6380", "0"},
		{"redis://localhost:6380", ""},
		{"redis://localhost:6380", "16"},
		{"redis://localhost:6380", "fifteen"},
		{"postgres://localhost:5433/vault", "15"},
	} {
		if _, err := TestRedisURL(testCase.redisURL, testCase.database); err == nil {
			t.Fatalf("TestRedisURL(%q, %q) error = nil, want refusal", testCase.redisURL, testCase.database)
		}
	}
}

func TestTestingEnvironmentFileUsesANonLiveRedisIndex(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("repositoryRoot() error = %v", err)
	}
	values, err := godotenv.Read(filepath.Join(root, ".env.testing"))
	if err != nil {
		t.Fatalf("read .env.testing: %v", err)
	}
	if _, err := TestRedisURL("redis://localhost:6380", values[redisDatabaseVariable]); err != nil {
		t.Fatalf(".env.testing %s must be a non-live index: %v", redisDatabaseVariable, err)
	}
}

func TestValidateConfigurationRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		configuration Configuration
		errorContains string
	}{
		{
			name: "development environment",
			configuration: Configuration{
				AppEnvironment: "local",
				DatabaseName:   "vault_test",
			},
			errorContains: "APP_ENV must be testing",
		},
		{
			name: "development database",
			configuration: Configuration{
				AppEnvironment: "testing",
				DatabaseName:   "vault",
			},
			errorContains: "is protected",
		},
		{
			name: "database outside the test prefix",
			configuration: Configuration{
				AppEnvironment: "testing",
				DatabaseName:   "vault_staging",
			},
			errorContains: "must start with vault_unit_test",
		},
		{
			name: "other _test database",
			configuration: Configuration{
				AppEnvironment: "testing",
				DatabaseName:   "markets_test",
			},
			errorContains: "must start with vault_unit_test",
		},
		{
			name: "empty database",
			configuration: Configuration{
				AppEnvironment: "testing",
				DatabaseName:   "",
			},
			errorContains: "database name is required",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateConfiguration(testCase.configuration)
			if err == nil {
				t.Fatal("ValidateConfiguration() error = nil")
			}
			if !strings.Contains(err.Error(), testCase.errorContains) {
				t.Fatalf("ValidateConfiguration() error = %q, want %q", err, testCase.errorContains)
			}
		})
	}
}
