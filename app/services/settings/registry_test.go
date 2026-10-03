package settings

import "testing"

func TestAccountSectionsDoNotShareNamesWithGroups(t *testing.T) {
	t.Parallel()

	names := map[string]bool{}
	var sawSecret bool
	var sawDecimal bool
	for _, group := range Registry() {
		if names[group.Name] {
			t.Fatalf("duplicate group %s", group.Name)
		}
		names[group.Name] = true
		if group.SectionName() == group.Name {
			t.Fatalf("section %s shares its name with the group", group.Name)
		}
		switch group.Scope {
		case ScopeAccount:
		case ScopePlatform:
			if group.Name != groupDepositScan {
				t.Fatalf("unexpected platform group %s", group.Name)
			}
		default:
			t.Fatalf("group %s has unknown scope %s", group.Name, group.Scope)
		}
		for _, definition := range group.Settings {
			if definition.Secret {
				sawSecret = true
			}
			if definition.Type == TypeDecimal {
				sawDecimal = true
			}
		}
	}
	if !sawSecret {
		t.Fatal("registry has no secret")
	}
	if !sawDecimal {
		t.Fatal("registry has no decimal")
	}
	limits, ok := FindGroup(groupAccountSweepLimits)
	if !ok || limits.ManagedBy != ManagedByPlatform || limits.Inherits != "sweep_limits" {
		t.Fatalf("sweep limits group = %+v present %v", limits, ok)
	}
	scan, ok := FindGroup(groupDepositScan)
	if !ok || scan.Scope != ScopePlatform || len(scan.Settings) != 3 {
		t.Fatalf("deposit scan group = %+v present %v", scan, ok)
	}
}
