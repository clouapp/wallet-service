package repositories_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}
