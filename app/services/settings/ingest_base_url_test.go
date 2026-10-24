package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
)

const (
	ingestSettingBaseURL = "https://push.wallets.example.com/waas"
	ingestEnvBaseURL     = "https://env.wallets.example.com"
)

func newIngestPushService(store Store, cache Cache, activity *recordingActivity) (*Service, uuid.UUID) {
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: cache, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	return service, actor
}

func TestSave_IngestPush_StoresAPublicHTTPSBaseURLInTheClear(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	service, actor := newIngestPushService(store, &memoryCache{}, activity)

	saved, err := service.SavePlatform(context.Background(), actor, groupIngestPush, map[string]any{
		keyIngestBaseURL: "  " + ingestSettingBaseURL + "  ",
	})
	if err != nil {
		t.Fatalf("save ingest_push: %v", err)
	}
	if got := store.rows[platformStoreKey(groupIngestPush)][keyIngestBaseURL]; got != ingestSettingBaseURL {
		t.Fatalf("stored base_url = %q, want the trimmed URL", got)
	}
	if saved.Name != groupIngestPush || len(saved.Fields) != 1 || saved.Fields[0].Value != ingestSettingBaseURL || saved.Fields[0].Secret {
		t.Fatalf("view = %+v", saved)
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d, want 1", len(activity.rows))
	}
	encoded, encodeErr := activity.rows[0].Metadata.Encode()
	if encodeErr != nil {
		t.Fatalf("metadata: %v", encodeErr)
	}
	if encoded != `{"fields":["base_url"],"group":"ingest_push"}` {
		t.Fatalf("activity metadata = %s", encoded)
	}
}

func TestSave_IngestPush_RefusesABaseURLAProviderCannotPostTo(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"http://wallets.example.com":            egress.ReasonScheme,
		"wallets.example.com":                   egress.ReasonInvalidURL,
		"https://":                              egress.ReasonInvalidURL,
		"https://user:pass@wallets.example.com": egress.ReasonUserinfo,
		"https://wallets.example.com/?a=1":      egress.ReasonBaseURL,
		"https://wallets.example.com?":          egress.ReasonBaseURL,
		"https://wallets.example.com#x":         egress.ReasonBaseURL,
		"https://wallets.example.com:99999":     egress.ReasonInvalidURL,
		"https://wallets.example.com:22":        egress.ReasonPort,
		"https://localhost":                     egress.ReasonLoopback,
		"https://localhost.":                    egress.ReasonLoopback,
		"https://127.0.0.1":                     egress.ReasonLoopback,
		"https://127.0.0.1.":                    egress.ReasonLoopback,
		"https://[::1]":                         egress.ReasonLoopback,
		"https://2130706433":                    egress.ReasonHostName,
		"https://0x7f000001":                    egress.ReasonHostName,
		"https://0177.0.0.1":                    egress.ReasonHostName,
		"https://127.1":                         egress.ReasonHostName,
		"https://169.254.169.254":               egress.ReasonLinkLocal,
		"https://169.254.169.254.":              egress.ReasonLinkLocal,
		"https://[64:ff9b::a9fe:a9fe]":          egress.ReasonLinkLocal,
		"https://metadata.google.internal":      egress.ReasonMetadata,
		"https://foo.internal":                  egress.ReasonHostName,
		"https://10.0.0.5":                      egress.ReasonPrivate,
		"https://100.64.0.1":                    egress.ReasonPrivate,
		"https://[::127.0.0.1]":                 egress.ReasonReserved,
		"https://0.1.2.3":                       egress.ReasonReserved,
		"https://255.255.255.255":               egress.ReasonReserved,
		"https://198.18.0.1":                    egress.ReasonReserved,
		"https://240.0.0.1":                     egress.ReasonReserved,
	}
	for base, reason := range cases {
		store := newMemoryStore()
		activity := &recordingActivity{}
		service, actor := newIngestPushService(store, &memoryCache{}, activity)

		_, err := service.SavePlatform(context.Background(), actor, groupIngestPush, map[string]any{keyIngestBaseURL: base})

		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%q: error = %v, want a validation error", base, err)
		}
		if messages := validation.Fields[keyIngestBaseURL]; len(messages) != 1 || messages[0] != reason {
			t.Fatalf("%q: fields = %#v, want the rule %q", base, validation.Fields, reason)
		}
		if _, stored := store.rows[platformStoreKey(groupIngestPush)]; stored {
			t.Fatalf("%q: an invalid base_url was stored", base)
		}
		if len(activity.rows) != 0 {
			t.Fatalf("%q: a refused write was recorded", base)
		}
	}
}

func TestValidate_IngestPush_AllowsBlankAndAPathPrefix(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"", "   ", "https://wallets.example.com", "https://wallets.example.com:8443/waas/"} {
		if err := validateIngestPush(map[string]string{keyIngestBaseURL: base}); err != nil {
			t.Fatalf("%q: %v", base, err)
		}
	}
	if err := validateIngestPush(map[string]string{}); err != nil {
		t.Fatalf("a missing base_url hands the URL back to the environment: %v", err)
	}
}

func TestEffective_IngestBaseURL_AValidSettingWinsOverTheEnvironment(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupIngestPush, map[string]string{keyIngestBaseURL: ingestSettingBaseURL})

	got, err := newTestService(store).EffectiveIngestBaseURL(context.Background(), ingestEnvBaseURL)

	if err != nil || got != (IngestBaseURL{URL: ingestSettingBaseURL, Source: IngestBaseURLFromSetting}) {
		t.Fatalf("effective = %+v, %v", got, err)
	}
}

func TestEffective_IngestBaseURL_FallsBackToTheEnvironment(t *testing.T) {
	t.Parallel()

	envOnly := IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv}
	cases := map[string]struct {
		store Store
		want  IngestBaseURL
	}{
		"missing row":      {store: newMemoryStore(), want: envOnly},
		"blank value":      {store: storeWith(map[string]string{keyIngestBaseURL: "   "}), want: envOnly},
		"another key only": {store: storeWith(map[string]string{"other": ingestSettingBaseURL}), want: envOnly},
	}
	for _, invalid := range []string{
		"http://10.0.0.5", "https://169.254.169.254.", "https://2130706433", "https://localhost.",
		"https://metadata.google.internal", "https://[64:ff9b::a9fe:a9fe]", "https://foo.internal",
	} {
		cases["invalid value stored outside the service: "+invalid] = struct {
			store Store
			want  IngestBaseURL
		}{
			store: storeWith(map[string]string{keyIngestBaseURL: invalid}),
			want:  IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv, SettingInvalid: true},
		}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := newTestService(tc.store).EffectiveIngestBaseURL(context.Background(), "  "+ingestEnvBaseURL+"  ")
			if err != nil || got != tc.want {
				t.Fatalf("effective = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestEffective_IngestBaseURL_AnUnreadSettingIsAnErrorBesideTheEnvironmentValue(t *testing.T) {
	t.Parallel()

	envOnly := IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv}
	cases := map[string]struct {
		service *Service
		ctx     context.Context
		cause   string
	}{
		"failed read": {
			service: NewService(Deps{Store: platformErrStore{err: errors.New("connection refused")}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}}),
			ctx:     context.Background(), cause: "connection refused",
		},
		"store without a platform reader": {
			service: NewService(Deps{Store: accountOnlyStore{}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}}),
			ctx:     context.Background(), cause: "platform reader is required",
		},
		"nil service": {service: nil, ctx: context.Background(), cause: "service is required"},
		"nil context": {
			service: newTestService(storeWith(map[string]string{keyIngestBaseURL: ingestSettingBaseURL})), ctx: nil, cause: "context is required",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := tc.service.EffectiveIngestBaseURL(tc.ctx, "  "+ingestEnvBaseURL+"  ")
			if !errors.Is(err, ErrIngestBaseURLUnread) || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("error = %v, want ErrIngestBaseURLUnread with %q", err, tc.cause)
			}
			if got != envOnly {
				t.Fatalf("effective = %+v, want the environment value %+v beside the error", got, envOnly)
			}
		})
	}
}

func TestEffective_IngestBaseURL_NothingConfiguredIsAnEmptyURLFromTheEnvironment(t *testing.T) {
	t.Parallel()

	got, err := newTestService(newMemoryStore()).EffectiveIngestBaseURL(context.Background(), "  ")

	if err != nil || got != (IngestBaseURL{Source: IngestBaseURLFromEnv}) {
		t.Fatalf("effective = %+v, %v", got, err)
	}
}

func TestEffective_IngestBaseURL_ASavedValueAppliesToTheNextRead(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	cache := &memoryCache{}
	service, actor := newIngestPushService(store, cache, &recordingActivity{})
	ctx := context.Background()

	if got, err := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); err != nil || got.Source != IngestBaseURLFromEnv {
		t.Fatalf("before the save = %+v, %v", got, err)
	}
	if _, cached, _ := cache.Get(platformCacheKey(groupIngestPush)); !cached {
		t.Fatal("the first read did not cache the group, so this test would not prove the save forgets it")
	}
	if _, err := service.SavePlatform(ctx, actor, groupIngestPush, map[string]any{keyIngestBaseURL: ingestSettingBaseURL}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, err := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); err != nil || got != (IngestBaseURL{URL: ingestSettingBaseURL, Source: IngestBaseURLFromSetting}) {
		t.Fatalf("after the save = %+v, %v", got, err)
	}
	if _, err := service.SavePlatform(ctx, actor, groupIngestPush, map[string]any{keyIngestBaseURL: ""}); err != nil {
		t.Fatalf("blank save: %v", err)
	}
	if got, err := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); err != nil || got != (IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv}) {
		t.Fatalf("after a blank save = %+v, %v", got, err)
	}
}

func storeWith(values map[string]string) *memoryStore {
	store := newMemoryStore()
	store.PutPlatform(groupIngestPush, values)
	return store
}

// accountOnlyStore reads and writes account groups and cannot read a platform group.
type accountOnlyStore struct{}

func (accountOnlyStore) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, nil
}

func (accountOnlyStore) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return nil
}
