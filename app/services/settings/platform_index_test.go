package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const platformIndexSecret = "platform-index-secret-value"

func TestPlatformIndex_ListsPlatformGroupsAndHidesSecrets(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailHost:     "127.0.0.1",
		keyMailPassword: "enc:v1:" + platformIndexSecret,
	})
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	view, err := service.PlatformIndex(context.Background(), actor)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if view.Permissions.View != "settings.view" || view.Permissions.Update != "settings.update" {
		t.Fatalf("permissions = %+v", view.Permissions)
	}
	got := groupNames(view)
	var want []string
	for _, group := range Registry() {
		if group.Scope == ScopePlatform {
			want = append(want, group.Name)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for _, name := range []string{groupAccountSecurity, groupAccountWebhooks, groupAccountSweepLimits} {
		if containsName(got, name) {
			t.Fatalf("account group %s was listed", name)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), platformIndexSecret) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the index included a secret")
	}
	smtp := groupByName(t, view, groupMailSMTP)
	password := fieldByKey(t, smtp, keyMailPassword)
	if !password.Secret || !password.IsSet || password.Value != nil {
		t.Fatal("the password field was not write-only")
	}
	host := fieldByKey(t, smtp, keyMailHost)
	if host.Value != "127.0.0.1" || host.Label == "" || host.Type != TypeString {
		t.Fatalf("host metadata = %+v", host)
	}
	if smtp.Scope != ScopePlatform || smtp.Section == "" {
		t.Fatalf("mail_smtp group = %+v", smtp)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
}

func TestPlatformIndex_ForbidsANonAdminBeforeReading(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	service := NewService(platformErrStore{err: errors.New("db down")}, prefixSealer{}, nopCache{}, discardActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{})
	_, err := service.PlatformIndex(context.Background(), actor)
	if !errors.Is(err, ErrPlatformViewForbidden) {
		t.Fatalf("non-admin = %v", err)
	}

	service = NewService(platformErrStore{err: errors.New("db down")}, prefixSealer{}, nopCache{}, discardActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	if _, err := service.PlatformIndex(context.Background(), actor); err == nil || err.Error() != "db down" {
		t.Fatalf("admin read = %v, want db down", err)
	}
}

func TestVisibleOnPlatformIndexKeepsAnUncataloguedViewPermission(t *testing.T) {
	t.Parallel()

	if visibleOnPlatformIndex(Group{Name: groupAccountSecurity, Scope: ScopeAccount, ViewPermission: "settings.view"}) {
		t.Fatal("an account group was listed")
	}
	if !visibleOnPlatformIndex(Group{Name: groupDepositScan, Scope: ScopePlatform}) {
		t.Fatal("a platform group with no view permission was omitted")
	}
	if !visibleOnPlatformIndex(Group{Name: groupMailSMTP, Scope: ScopePlatform, ViewPermission: "mail.view"}) {
		t.Fatal("mail.view is not in a platform catalog; the platform_admins row stands in")
	}
	if !visibleOnPlatformIndex(Group{Name: groupProviderAlchemy, Scope: ScopePlatform, ViewPermission: "providers.view"}) {
		t.Fatal("providers.view is not in a platform catalog; the platform_admins row stands in")
	}
	if platformAdminCoversViewPermission("  ") {
		t.Fatal("a blank view permission was treated as a named one")
	}
}

func groupNames(view RegistryView) []string {
	var names []string
	for _, section := range view.Sections {
		for _, block := range section.Blocks {
			for _, group := range block.Groups {
				names = append(names, group.Name)
			}
		}
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func groupByName(t *testing.T, view RegistryView, name string) GroupView {
	t.Helper()
	group, ok := groupByNameView(view, name)
	if !ok {
		t.Fatalf("group %s missing", name)
	}
	return group
}

func groupByNameView(view RegistryView, name string) (GroupView, bool) {
	for _, section := range view.Sections {
		for _, block := range section.Blocks {
			for _, group := range block.Groups {
				if group.Name == name {
					return group, true
				}
			}
		}
	}
	return GroupView{}, false
}
