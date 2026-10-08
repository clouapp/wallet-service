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

func TestPlatform_Group_AdminSeesDefaultsAndHidesASecret(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &recordingActivity{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
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
	service = NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
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

func TestPlatform_Group_AdminReadsProviderCredentialsWithoutTheSecret(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := newMemoryStore()
	const sealed = "platform-provider-secret-value"
	for _, group := range providerCredentialGroups() {
		store.PutPlatform(group.name, map[string]string{
			group.secretKey: "enc:v1:" + sealed,
			"enabled":       "true",
		})
	}
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	for _, group := range providerCredentialGroups() {
		view, err := service.PlatformGroup(context.Background(), actor, group.name)
		if err != nil {
			t.Fatalf("%s: %v", group.name, err)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("%s view: %v", group.name, err)
		}
		if strings.Contains(string(encoded), sealed) || strings.Contains(string(encoded), "enc:v1:") {
			t.Fatalf("%s included a secret", group.name)
		}
		secret := fieldByKey(t, view, group.secretKey)
		if !secret.Secret || !secret.IsSet || secret.Value != nil {
			t.Fatalf("%s secret field = %+v", group.name, secret)
		}
		enabled := fieldByKey(t, view, "enabled")
		if enabled.Secret || enabled.Value != true {
			t.Fatalf("%s enabled = %+v", group.name, enabled)
		}
	}
}

type providerCredentialGroup struct {
	name      string
	secretKey string
}

func providerCredentialGroups() []providerCredentialGroup {
	groups := make([]providerCredentialGroup, 0, len(webhookProviderGroupNames())+1+len(priceProviderGroupNames()))
	for _, name := range webhookProviderGroupNames() {
		key := keyProviderAPIKey
		if name == groupProviderAlchemy {
			key = keyProviderAuthToken
		}
		groups = append(groups, providerCredentialGroup{name: name, secretKey: key})
	}
	groups = append(groups, providerCredentialGroup{name: groupProviderEtherscan, secretKey: keyProviderAPIKey})
	for _, name := range priceProviderGroupNames() {
		groups = append(groups, providerCredentialGroup{name: name, secretKey: keyPriceAPIKey})
	}
	return groups
}

func TestGet_Group_DefaultTakesTheTypeShape(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	view, err := service.PlatformGroup(context.Background(), actor, groupPriceLookup)
	if err != nil {
		t.Fatalf("unstored price lookup: %v", err)
	}
	order := fieldByKey(t, view, keyProviderOrder)
	items, ok := order.Value.([]string)
	if !ok || order.IsSet || len(items) != 0 {
		t.Fatalf("provider order = %#v set %v", order.Value, order.IsSet)
	}
	definition, found := Find(groupPriceLookup, keyProviderOrder)
	if !found || len(order.Options) != len(definition.Options) {
		t.Fatal("provider order options were not returned with the default")
	}
}

func TestSettingsService_Bool_DefaultsToDisabled(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	view, err := service.PlatformGroup(context.Background(), actor, groupPriceCoinGecko)
	if err != nil {
		t.Fatalf("unstored provider: %v", err)
	}
	enabled := fieldByKey(t, view, keyPriceEnabled)
	if enabled.Value != false || enabled.IsSet || enabled.Secret {
		t.Fatalf("enabled = %+v", enabled)
	}
	secret := fieldByKey(t, view, keyPriceAPIKey)
	if secret.IsSet || secret.Value != nil || !secret.Secret {
		t.Fatal("an unstored provider key was treated as set")
	}
}

func TestPlatform_Group_NotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	admins := &countingPlatformAdmins{}
	service := NewService(Deps{Store: platformErrStore{err: errors.New("db down")}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}}).
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
