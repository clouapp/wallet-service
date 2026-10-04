package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const platformGroupSecret = "platform-group-secret-value"

type countingPlatformAdmins struct {
	allow bool
	calls int
}

func (a *countingPlatformAdmins) Contains(context.Context, uuid.UUID) (bool, error) {
	a.calls++
	return a.allow, nil
}

func TestPlatformGroup_AdminSeesDefaultsAndHidesASecret(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &recordingActivity{}
	service := NewService(newMemoryStore(), prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	empty, err := service.PlatformGroup(context.Background(), actor, groupMailSMTP)
	if err != nil {
		t.Fatalf("unstored group: %v", err)
	}
	password := fieldByKey(t, empty, keyMailPassword)
	if !password.Secret || password.IsSet || password.Value != nil {
		t.Fatal("unstored password was not write-only")
	}
	port := fieldByKey(t, empty, keyMailPort)
	if port.Value != defaultMailPort || port.Secret {
		t.Fatalf("unstored port = %+v", port)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailHost:     "127.0.0.1",
		keyMailPassword: "enc:v1:" + platformGroupSecret,
	})
	service = NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	view, err := service.PlatformGroup(context.Background(), actor, "  "+groupMailSMTP+"  ")
	if err != nil {
		t.Fatalf("stored group: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), platformGroupSecret) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the group included a secret")
	}
	password = fieldByKey(t, view, keyMailPassword)
	if !password.Secret || !password.IsSet || password.Value != nil {
		t.Fatal("the password field was not write-only")
	}
	host := fieldByKey(t, view, keyMailHost)
	if host.Value != "127.0.0.1" || host.Secret {
		t.Fatalf("host = %+v", host)
	}
	if view.Name != groupMailSMTP || view.Scope != ScopePlatform {
		t.Fatalf("group = %+v", view)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
}

func TestPlatformGroup_NotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	admins := &countingPlatformAdmins{}
	service := NewService(platformErrStore{err: errors.New("db down")}, prefixSealer{}, nopCache{}, discardActivity{}).
		WithPlatformAdmins(admins)

	for _, name := range []string{"no-such-group", groupAccountSecurity} {
		_, err := service.PlatformGroup(context.Background(), actor, name)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s = %v, want not found", name, err)
		}
	}
	if admins.calls != 0 {
		t.Fatalf("unknown group asked the admin gate %d times", admins.calls)
	}

	_, err := service.PlatformGroup(context.Background(), actor, groupMailSMTP)
	if !errors.Is(err, ErrPlatformViewForbidden) {
		t.Fatalf("non-admin on a known group = %v, want forbidden", err)
	}
	if admins.calls != 1 {
		t.Fatalf("admin checks = %d, want 1", admins.calls)
	}
}
