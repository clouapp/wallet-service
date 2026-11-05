package ramps_test

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// providerStub answers the real adapters' calls for the whole package: the
// base URL has to be in the environment before the application boots.
var providerStub *bridgeStub

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	providerStub = newBridgeStub()
	server := httptest.NewServer(providerStub)
	os.Setenv("RAMP_BRIDGE_BASE_URL", server.URL)
	os.Setenv("RAMP_OZAV_BASE_URL", server.URL+"/ozav")
	os.Setenv("RAMP_BLINDPAY_BASE_URL", server.URL+"/blindpay")
	providerStub.base = server.URL
	testutil.BootApp()
	code := m.Run()
	server.Close()
	os.Exit(code)
}
