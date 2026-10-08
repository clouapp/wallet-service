package features

import (
	"testing"
)

func TestCatalog_Names_TheAccountFlagsAndTheirDefaults(t *testing.T) {
	t.Parallel()

	want := []string{
		FlagAPIRequestSignatureRequired,
		FlagDepositScanEnabled,
		FlagSweepEnabled,
		FlagUser2FARequired,
		FlagWalletCreationEnabled,
		FlagWebhookDeliveryEnabled,
		FlagWithdrawalsEnabled,
	}
	got := All()
	if len(got) != len(want) {
		t.Fatalf("catalog length = %d, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	var previous string
	for i, definition := range got {
		if definition.Key != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, definition.Key, want[i])
		}
		if seen[definition.Key] {
			t.Fatalf("duplicate key %q", definition.Key)
		}
		seen[definition.Key] = true
		if definition.Key < previous {
			t.Fatalf("catalog is not sorted: %q before %q", previous, definition.Key)
		}
		previous = definition.Key
		wantDefault := definition.Key == FlagDepositScanEnabled || definition.Key == FlagSweepEnabled || definition.Key == FlagWalletCreationEnabled || definition.Key == FlagWebhookDeliveryEnabled || definition.Key == FlagWithdrawalsEnabled
		if definition.Default != wantDefault {
			t.Fatalf("%s default = %v, want %v", definition.Key, definition.Default, wantDefault)
		}
		if !definition.AppliesTo(ScopeAccount) {
			t.Fatalf("%s does not apply to the account scope", definition.Key)
		}
		if !definition.AppliesTo(ScopeGlobal) {
			t.Fatalf("%s does not apply to the global scope", definition.Key)
		}
		if definition.Label == "" || definition.Description == "" {
			t.Fatalf("%s is missing label or description", definition.Key)
		}
	}

	if _, ok := Find("not-a-flag"); ok {
		t.Fatal("unknown key must not resolve")
	}
	if ForAccount()[0].Key != FlagAPIRequestSignatureRequired {
		t.Fatalf("ForAccount()[0] = %q", ForAccount()[0].Key)
	}
	global := ForGlobal()
	if len(global) != len(got) {
		t.Fatalf("global catalog length = %d, want %d", len(global), len(got))
	}
	for i, definition := range global {
		if definition.Key != got[i].Key || definition.Default != got[i].Default {
			t.Fatalf("global %s default = %v, account default %v", definition.Key, definition.Default, got[i].Default)
		}
	}
}
