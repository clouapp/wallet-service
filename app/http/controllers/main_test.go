package controllers_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/testenv"
)

// TestMain boots the full Goravel application (including routes) once
// before any controller integration test runs.
//
// API_KEY_SECRET is exported because middleware.HMACAuth still reads it
// from the security config, even though no test in this package signs
// requests with the legacy HMAC scheme. The external API tests under
// /api/v1/* authenticate via JWT minted by middleware.MintAPIToken, so
// the specific value here doesn't affect them — we just need the config
// to be non-empty to avoid a bootstrap-time validation error.
func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("API_KEY_SECRET") == "" {
		os.Setenv("API_KEY_SECRET", "test-api-secret-for-unit-tests")
	}
	// AWS_DEFAULT_REGION must be set for AWS SDK to initialize without error
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	_ = container.Get()
	os.Exit(m.Run())
}
