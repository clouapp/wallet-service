package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
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

	for _, base := range []string{
		"http://wallets.example.com", "wallets.example.com", "https://", "https://user:pass@wallets.example.com",
		"https://wallets.example.com/?a=1", "https://wallets.example.com?", "https://wallets.example.com#x",
		"https://localhost", "https://127.0.0.1", "https://10.0.0.5", "https://169.254.169.254", "https://[::1]",
		"https://100.64.0.1", "https://wallets.example.com:99999",
	} {
		store := newMemoryStore()
		activity := &recordingActivity{}
		service, actor := newIngestPushService(store, &memoryCache{}, activity)

		_, err := service.SavePlatform(context.Background(), actor, groupIngestPush, map[string]any{keyIngestBaseURL: base})

		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%q: error = %v, want a validation error", base, err)
		}
		if messages := validation.Fields[keyIngestBaseURL]; len(messages) != 1 || messages[0] != ingestBaseURLInvalid {
			t.Fatalf("%q: fields = %#v", base, validation.Fields)
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

	got := newTestService(store).EffectiveIngestBaseURL(context.Background(), ingestEnvBaseURL)

	if got != (IngestBaseURL{URL: ingestSettingBaseURL, Source: IngestBaseURLFromSetting}) {
		t.Fatalf("effective = %+v", got)
	}
}

func TestEffective_IngestBaseURL_FallsBackToTheEnvironment(t *testing.T) {
	t.Parallel()

	envOnly := IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv}
	cases := map[string]struct {
		service *Service
		ctx     context.Context
		want    IngestBaseURL
	}{
		"missing row": {service: newTestService(newMemoryStore()), ctx: context.Background(), want: envOnly},
		"blank value": {
			service: newTestService(storeWith(map[string]string{keyIngestBaseURL: "   "})), ctx: context.Background(), want: envOnly,
		},
		"another key only": {
			service: newTestService(storeWith(map[string]string{"other": ingestSettingBaseURL})), ctx: context.Background(), want: envOnly,
		},
		"invalid value stored outside the service": {
			service: newTestService(storeWith(map[string]string{keyIngestBaseURL: "http://10.0.0.5"})), ctx: context.Background(), want: envOnly,
		},
		"failed read": {
			service: NewService(Deps{Store: platformErrStore{err: errors.New("connection refused")}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}}),
			ctx:     context.Background(), want: envOnly,
		},
		"store without a platform reader": {
			service: NewService(Deps{Store: accountOnlyStore{}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}}),
			ctx:     context.Background(), want: envOnly,
		},
		"nil service": {service: nil, ctx: context.Background(), want: envOnly},
		"nil context": {service: newTestService(storeWith(map[string]string{keyIngestBaseURL: ingestSettingBaseURL})), ctx: nil, want: envOnly},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := tc.service.EffectiveIngestBaseURL(tc.ctx, "  "+ingestEnvBaseURL+"  ")
			if got != tc.want {
				t.Fatalf("effective = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestEffective_IngestBaseURL_NothingConfiguredIsAnEmptyURLFromTheEnvironment(t *testing.T) {
	t.Parallel()

	got := newTestService(newMemoryStore()).EffectiveIngestBaseURL(context.Background(), "  ")

	if got != (IngestBaseURL{Source: IngestBaseURLFromEnv}) {
		t.Fatalf("effective = %+v", got)
	}
}

func TestEffective_IngestBaseURL_ASavedValueAppliesToTheNextRead(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	cache := &memoryCache{}
	service, actor := newIngestPushService(store, cache, &recordingActivity{})
	ctx := context.Background()

	if got := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); got.Source != IngestBaseURLFromEnv {
		t.Fatalf("before the save = %+v", got)
	}
	if _, cached, _ := cache.Get(platformCacheKey(groupIngestPush)); !cached {
		t.Fatal("the first read did not cache the group, so this test would not prove the save forgets it")
	}
	if _, err := service.SavePlatform(ctx, actor, groupIngestPush, map[string]any{keyIngestBaseURL: ingestSettingBaseURL}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); got != (IngestBaseURL{URL: ingestSettingBaseURL, Source: IngestBaseURLFromSetting}) {
		t.Fatalf("after the save = %+v", got)
	}
	if _, err := service.SavePlatform(ctx, actor, groupIngestPush, map[string]any{keyIngestBaseURL: ""}); err != nil {
		t.Fatalf("blank save: %v", err)
	}
	if got := service.EffectiveIngestBaseURL(ctx, ingestEnvBaseURL); got != (IngestBaseURL{URL: ingestEnvBaseURL, Source: IngestBaseURLFromEnv}) {
		t.Fatalf("after a blank save = %+v", got)
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
