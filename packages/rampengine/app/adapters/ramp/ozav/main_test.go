package ozav

import (
	"os"
	"testing"

	"github.com/goravel/framework/testing/mock"
)

// TestMain binds the log facade once for the package.
//
// The transport reports latency and outcome on every call, which means every
// test in here touches the logger. Binding it per test would be the same
// three lines repeated across each one.
func TestMain(m *testing.M) {
	mock.Factory().Log()
	os.Exit(m.Run())
}
