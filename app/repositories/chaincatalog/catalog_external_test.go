package chaincatalog_test

import (
	"os"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}

func newCatalog() *chaincatalog.Catalog {
	return chaincatalog.New(facades.Config(), facades.Crypt())
}
