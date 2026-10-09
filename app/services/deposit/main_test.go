package deposit_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// The booted application imports this package, so the TestMain lives in the
// external test package; it still boots Goravel once for the whole test binary.
func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}
