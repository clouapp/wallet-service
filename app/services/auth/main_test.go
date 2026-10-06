package auth_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

// TestMain boots Goravel so the cache-backed challenge store can be tested
// against the test Redis index (.env.testing REDIS_DB, never the live one).
// The pure tests in this package do not depend on the boot.
func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		_ = os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	os.Exit(m.Run())
}
