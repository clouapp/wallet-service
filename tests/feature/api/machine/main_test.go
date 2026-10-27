package machine

import (
	"os"
	"testing"

	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// testProvisioningKey is the TENANT_PROVISIONING_KEY the app boots with here.
const testProvisioningKey = "tenant-provisioning-key-for-the-feature-suite"

// TestMain boots the full application once, with a provisioning key so the
// machine route is on.
func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	os.Setenv("TENANT_PROVISIONING_KEY", testProvisioningKey)
	testutil.BootApp()
	os.Exit(m.Run())
}
