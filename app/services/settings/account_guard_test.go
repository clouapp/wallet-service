package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/policies"
)

func TestAccountSettingsCatalogDeclaresTheAccountGuard(t *testing.T) {
	t.Parallel()

	catalog := AccountSettingsCatalog()
	if catalog.Read != policies.PermSettingsRead || catalog.Write != policies.PermSettingsWrite {
		t.Fatalf("catalog = %+v", catalog)
	}
	if catalog.Read != "settings.read" || catalog.Write != "settings.write" {
		t.Fatalf("catalog names = %q %q", catalog.Read, catalog.Write)
	}
	if err := requireAccountGuard(catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Read == policies.PermSettingsView || catalog.Write == policies.PermSettingsUpdate {
		t.Fatal("the account guard reused the platform pair")
	}

	security, ok := FindGroup(groupAccountSecurity)
	if !ok {
		t.Fatal("account_security is missing")
	}
	if security.ViewPermission != "" || security.UpdatePermission != "" ||
		security.ViewPermission == "settings.security.write" || security.UpdatePermission == "settings.security.write" {
		t.Fatalf("account_security permissions = %q %q", security.ViewPermission, security.UpdatePermission)
	}
}

func TestOwnerAndAdminStillWriteAnAccountGroup(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	service := newTestService(newMemoryStore())
	ctx := context.Background()
	writes := []struct {
		role    string
		minutes int
	}{
		{role: "owner", minutes: 45},
		{role: "admin", minutes: 60},
	}
	for _, write := range writes {
		view, err := service.Save(ctx, accountID, uuid.New(), write.role, groupAccountSecurity, map[string]any{
			keySessionIdleMinutes: write.minutes,
		})
		if err != nil {
			t.Fatalf("%s save: %v", write.role, err)
		}
		if !view.CanUpdate || view.Name != groupAccountSecurity {
			t.Fatalf("%s view = %+v", write.role, view)
		}
		idle := fieldByKey(t, view, keySessionIdleMinutes)
		if idle.Value != write.minutes {
			t.Fatalf("%s idle = %+v", write.role, idle)
		}
	}

	read, err := service.AccountGroup(ctx, accountID, "auditor", groupAccountSecurity)
	if err != nil {
		t.Fatalf("auditor read: %v", err)
	}
	if read.CanUpdate {
		t.Fatal("auditor can write account_security")
	}
	idle := fieldByKey(t, read, keySessionIdleMinutes)
	if idle.Value != 60 {
		t.Fatalf("auditor idle = %+v", idle)
	}
	if _, err := service.Save(ctx, accountID, uuid.New(), "auditor", groupAccountSecurity, map[string]any{
		keySessionIdleMinutes: 12,
	}); !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("auditor save = %v", err)
	}
	kept, err := service.AccountGroup(ctx, accountID, "owner", groupAccountSecurity)
	if err != nil {
		t.Fatalf("owner reread: %v", err)
	}
	if fieldByKey(t, kept, keySessionIdleMinutes).Value != 60 {
		t.Fatal("auditor write changed the stored minutes")
	}

	if _, err := service.Save(ctx, accountID, uuid.New(), "user", groupAccountSecurity, map[string]any{
		keySessionIdleMinutes: 12,
	}); !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("user save = %v", err)
	}

	registry, err := service.Registry(ctx, accountID, "auditor")
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if registry.Permissions.View != "settings.read" || registry.Permissions.Update != "settings.write" {
		t.Fatalf("registry permissions = %+v", registry.Permissions)
	}
}
