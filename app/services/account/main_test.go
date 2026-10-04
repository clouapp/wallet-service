package account_test

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/testenv"
)

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		_ = os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	os.Exit(m.Run())
}
