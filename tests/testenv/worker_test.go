package testenv

import (
	"os"
	"strings"
	"testing"
)

func TestWorkerDatabaseNamesStayInsideTheTestPrefix(t *testing.T) {
	name := WorkerDatabaseName(DefaultTestDatabaseName, 3)
	if name != "vault_unit_test_p3" {
		t.Fatalf("WorkerDatabaseName = %q", name)
	}
	if err := ValidateConfiguration(Configuration{AppEnvironment: "testing", DatabaseName: name}); err != nil {
		t.Fatalf("a worker clone must pass the guard: %v", err)
	}
	for _, other := range []string{DefaultTestDatabaseName, "vault_unit_test_p", "vault_unit_test_p0", "vault_unit_test_p1x", "vault_test_p1", "vault_unit_test_pr95"} {
		if IsWorkerDatabase(DefaultTestDatabaseName, other) {
			t.Fatalf("IsWorkerDatabase(%q) = true", other)
		}
	}
	if !IsWorkerDatabase(DefaultTestDatabaseName, "vault_unit_test_p12") {
		t.Fatal("vault_unit_test_p12 is a worker clone")
	}
}

func TestValidateTemplateRefusesLiveDatabasesAndClones(t *testing.T) {
	t.Setenv("APP_ENV", "testing")
	if err := ValidateTemplate(DefaultTestDatabaseName); err != nil {
		t.Fatalf("ValidateTemplate(%s): %v", DefaultTestDatabaseName, err)
	}
	for _, template := range []string{DevelopmentDatabaseName, E2EDatabaseName, "markets_test", "vault_unit_test_p2", ""} {
		if err := ValidateTemplate(template); err == nil {
			t.Fatalf("ValidateTemplate(%q) = nil, want a refusal", template)
		}
	}
}

func TestWorkerCountIsBounded(t *testing.T) {
	for raw, want := range map[string]int{"": 1, "1": 1, "6": 6} {
		t.Setenv(WorkersVariable, raw)
		if got, err := workerCount(); err != nil || got != want {
			t.Fatalf("workerCount(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "7", "-1", "four"} {
		t.Setenv(WorkersVariable, raw)
		if _, err := workerCount(); err == nil {
			t.Fatalf("workerCount(%q) = nil error", raw)
		}
	}
}

func TestLockWorkerSlotTakesTheFirstFreeSlot(t *testing.T) {
	t.Setenv(LockDirVariable, t.TempDir())
	defer func() { workerLock = nil }()

	first, err := lockWorkerSlot(2)
	if err != nil || first != 1 {
		t.Fatalf("first slot = %d, %v", first, err)
	}
	held := workerLock
	defer held.Close()
	second, err := lockWorkerSlot(2)
	if err != nil || second != 2 {
		t.Fatalf("second slot = %d, %v; slot 1 is held", second, err)
	}
	_ = workerLock.Close()
	if !strings.HasSuffix(held.Name(), workerLockPrefix+"1.lock") {
		t.Fatalf("slot file = %s", held.Name())
	}
}

func TestSetUpWorkerRefusesAProtectedTemplateBeforeTouchingAnything(t *testing.T) {
	t.Setenv("APP_ENV", "testing")
	t.Setenv(TemplateVariable, E2EDatabaseName)
	t.Setenv(LockDirVariable, t.TempDir())
	t.Setenv(databaseVariable, DefaultTestDatabaseName)

	err := setUpWorker()
	if err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Fatalf("setUpWorker() = %v, want the protected refusal", err)
	}
	if got := os.Getenv(databaseVariable); got != DefaultTestDatabaseName {
		t.Fatalf("DB_DATABASE moved to %q", got)
	}
}
