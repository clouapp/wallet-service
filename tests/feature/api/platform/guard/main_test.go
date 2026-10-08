package guard

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

// TestMain boots the full Goravel application (including routes) once
// before any controller integration test runs.
func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	// AWS_DEFAULT_REGION must be set for AWS SDK to initialize without error
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	os.Exit(m.Run())
}
