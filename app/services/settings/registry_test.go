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
			if group.Name != groupDepositScan && group.Name != groupWebhookDelivery && group.Name != groupSweepLimits {
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
	if !ok || limits.ManagedBy != ManagedByPlatform || limits.Inherits != groupSweepLimits {
		t.Fatalf("sweep limits group = %+v present %v", limits, ok)
	}
	platformLimits, ok := FindGroup(groupSweepLimits)
	if !ok || platformLimits.Scope != ScopePlatform || platformLimits.UpdatePermission != "" ||
		platformLimits.SectionName() != sectionSweep || len(platformLimits.Settings) != len(limits.Settings) {
		t.Fatalf("platform sweep limits = %+v present %v", platformLimits, ok)
	}
	for i, definition := range limits.Settings {
		if platformLimits.Settings[i].Key != definition.Key {
			t.Fatalf("platform key %s, account key %s", platformLimits.Settings[i].Key, definition.Key)
		}
	}
	scan, ok := FindGroup(groupDepositScan)
	if !ok || scan.Scope != ScopePlatform || len(scan.Settings) != 3 {
		t.Fatalf("deposit scan group = %+v present %v", scan, ok)
	}
	delivery, ok := FindGroup(groupWebhookDelivery)
	if !ok || delivery.Scope != ScopePlatform || len(delivery.Settings) != 2 || delivery.UpdatePermission != "" {
		t.Fatalf("webhook delivery group = %+v present %v", delivery, ok)
	}
}

func TestFindGroupUnknown(t *testing.T) {
	t.Parallel()

	if _, ok := FindGroup("nope"); ok {
		t.Fatal("an unknown group was found")
	}
	if _, ok := Find("nope", "port"); ok {
		t.Fatal("an unknown group returned a definition")
	}
}

func TestSectionNameDefaultsToTheGroupName(t *testing.T) {
	t.Parallel()

	group := Group{Name: "auth"}
	if got := group.SectionName(); got != "auth" {
		t.Fatalf("section = %q", got)
	}
}

func TestRegistryEveryGroupReportsASection(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		section := group.SectionName()
		if section == "" {
			t.Fatalf("group %s reports no section", group.Name)
		}
		if section == group.Name {
			continue
		}
		if _, taken := FindGroup(section); taken {
			t.Fatalf("section %s of group %s is also a group name", section, group.Name)
		}
	}
}

func TestRegistryEverySecretDeclaresAType(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		for _, definition := range group.Settings {
			if definition.Secret && definition.Type == "" {
				t.Fatalf("%s.%s is a secret with no type", group.Name, definition.Key)
			}
		}
	}
}

func TestEveryRegistryDefaultCastsThroughItsType(t *testing.T) {
	t.Parallel()

	for _, group := range Registry() {
		for _, definition := range group.Settings {
			if definition.Default == nil {
				t.Fatalf("%s.%s has no default", group.Name, definition.Key)
			}
			stored, err := castIn(definition.Default(), definition)
			if err != nil {
				t.Fatalf("%s.%s default: %v", group.Name, definition.Key, err)
			}
			if stored != defaultStored(definition) {
				t.Fatalf("%s.%s cast %q, defaultStored %q", group.Name, definition.Key, stored, defaultStored(definition))
			}
			if definition.Type == "" {
				t.Fatalf("%s.%s has no type", group.Name, definition.Key)
			}
		}
	}
}

func TestEverySecretGroupDeclaresAPermission(t *testing.T) {
	t.Parallel()

	var sawSecret bool
	for _, group := range Registry() {
		secret := false
		for _, definition := range group.Settings {
			if definition.Secret {
				secret = true
				sawSecret = true
			}
		}
		if !secret {
			continue
		}
		if group.ViewPermission == "" || group.UpdatePermission == "" {
			t.Fatalf("secret group %s declares no permission", group.Name)
		}
	}
	if !sawSecret {
		t.Fatal("registry has no secret")
	}
	webhooks, ok := FindGroup(groupAccountWebhooks)
	if !ok || webhooks.ViewPermission != permAccountWebhooksView || webhooks.UpdatePermission != permAccountWebhooksUpdate {
		t.Fatalf("webhooks permissions = %q %q present %v", webhooks.ViewPermission, webhooks.UpdatePermission, ok)
	}
}

func TestSigningSecretIsASecretString(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupAccountWebhooks, keySigningSecret)
	if !ok || !definition.Secret || definition.Type != TypeString {
		t.Fatalf("signing secret = %+v present %v", definition, ok)
	}
	idle, ok := Find(groupAccountSecurity, keySessionIdleMinutes)
	if !ok || idle.Type != TypeInt {
		t.Fatalf("session idle = %+v present %v", idle, ok)
	}
}
