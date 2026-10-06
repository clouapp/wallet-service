package testutil

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/tests/testenv"
)

func TestBootTestLoadsDedicatedTestingEnvironment(t *testing.T) {
	BootTest()

	if got := os.Getenv("APP_ENV"); got != "testing" {
		t.Fatalf("APP_ENV = %q, want testing", got)
	}
	expectedDatabase := testenv.DefaultTestDatabaseName
	if override := strings.TrimSpace(os.Getenv(testenv.DatabaseOverrideVariable)); override != "" {
		expectedDatabase = override
	}
	got := facades.Config().GetString("database.connections.postgres.database")
	if testenv.WorkerMode() {
		if !testenv.IsWorkerDatabase(strings.TrimSpace(os.Getenv(testenv.TemplateVariable)), got) {
			t.Fatalf("database = %q, want a worker clone of %s", got, os.Getenv(testenv.TemplateVariable))
		}
	} else if got != expectedDatabase {
		t.Fatalf("database = %q, want %s", got, expectedDatabase)
	}

	expectedPort, err := strconv.Atoi(os.Getenv("DB_PORT"))
	if err != nil {
		t.Fatalf("DB_PORT is invalid: %v", err)
	}
	if got := facades.Config().GetInt("database.connections.postgres.port"); got != expectedPort {
		t.Fatalf("database port = %d, want %d", got, expectedPort)
	}
}
