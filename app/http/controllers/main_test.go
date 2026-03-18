package controllers_test

import (
	"os"
	"testing"

	"github.com/macromarkets/vault/app/container"
	"github.com/macromarkets/vault/bootstrap"
)

// TestMain boots the full Goravel application (including routes) once
// before any controller integration test runs.
func TestMain(m *testing.M) {
	os.Setenv("API_KEY_SECRET", testAPISecret)
	// AWS_DEFAULT_REGION must be set for AWS SDK to initialize without error
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	container.Boot()
	bootstrap.Boot()
	os.Exit(m.Run())
}
