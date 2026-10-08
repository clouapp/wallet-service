package bootstrap_test

import (
	"os"
	"testing"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

// app is the application booted once for every test of this package: a
// second Boot would register the providers again in the same process.
var app contractsfoundation.Application

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	app = bootstrap.Boot()
	os.Exit(m.Run())
}
