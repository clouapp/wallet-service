package ramp

import (
	"os"
	"testing"

	"github.com/goravel/framework/testing/mock"
)

// TestMain binds the log facade once for the package: the router reports every
// resolution and every failover, so any test that routes touches the logger.
func TestMain(m *testing.M) {
	mock.Factory().Log()
	os.Exit(m.Run())
}
