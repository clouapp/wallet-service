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
	expectedDatabase := "vault_test"
	if override := strings.TrimSpace(os.Getenv(testenv.DatabaseOverrideVariable)); override != "" {
		expectedDatabase = override
	}
	if got := facades.Config().GetString("database.connections.postgres.database"); got != expectedDatabase {
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
