package tests

import (
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/bootstrap"
)

// Boot initializes Goravel for testing
// This is separate from the main bootstrap to avoid import cycles
func Boot(t *testing.T) {
	t.Helper()

	// Boot Goravel application for testing
	if facades.Config() == nil {
		bootstrap.Boot()
	}
}
