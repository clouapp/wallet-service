package ramps_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	testutil.BootApp()
	os.Exit(m.Run())
}
