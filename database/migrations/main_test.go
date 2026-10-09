package migrations_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// The whitebox test of this package reads the database; booting the application
// here also makes the Makefile run the package with the integration tests.
func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}
