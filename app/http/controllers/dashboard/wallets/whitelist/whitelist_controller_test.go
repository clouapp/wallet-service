package whitelist

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
)

func TestNew_WhitelistController_RequiresTheWhitelistRecords(t *testing.T) {
	const want = "dashboard whitelist controller: whitelist service is required"
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v", got)
		}
	}()

	NewWhitelistController(nil)

	t.Fatal("expected a panic")
}

func TestNew_WhitelistController_KeepsTheWhitelistRecords(t *testing.T) {
	entries := &walletrecords.Whitelist{}

	if NewWhitelistController(entries).entries != entries {
		t.Fatal("whitelist controller did not keep the whitelist service")
	}
}
