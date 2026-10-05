package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	mailSESKeyFixture    = "ses-access-key-fixture"
	mailSESSecretFixture = "ses-secret-fixture"
	mailMailgunFixture   = "mailgun-secret-fixture"
	mailResendFixture    = "resend-api-key-fixture"
	mailPostmarkFixture  = "postmark-token-fixture"
)

type mailProviderCase struct {
	group    string
	body     map[string]any
	secrets  []string
	fixtures []string
	public   map[string]string
	activity string
}

func mailProviderCases() []mailProviderCase {
	return []mailProviderCase{
		{
			group: groupMailSES,
			body: map[string]any{
				keyMailProviderKey:    mailSESKeyFixture,
				keyMailProviderSecret: mailSESSecretFixture,
				keyMailRegion:         "us-east-1",
				keyMailFromAddress:    "ses-from@example.test",
				keyMailFromName:       "SES",
			},
			secrets:  []string{keyMailProviderKey, keyMailProviderSecret},
			fixtures: []string{mailSESKeyFixture, mailSESSecretFixture},
			public: map[string]string{
				keyMailRegion:      "us-east-1",
				keyMailFromAddress: "ses-from@example.test",
				keyMailFromName:    "SES",
			},
			activity: `{"fields":["from_address","from_name","key","region","secret"],"group":"mail_ses"}`,
		},
		{
			group: groupMailMailgun,
			body: map[string]any{
				keyMailDomain:         "mg.example.test",
				keyMailProviderSecret: mailMailgunFixture,
				keyMailEndpoint:       "api.mailgun.net",
				keyMailFromAddress:    "mg-from@example.test",
				keyMailFromName:       "Mailgun",
			},
			secrets:  []string{keyMailProviderSecret},
			fixtures: []string{mailMailgunFixture},
			public: map[string]string{
				keyMailDomain:      "mg.example.test",
				keyMailEndpoint:    "api.mailgun.net",
				keyMailFromAddress: "mg-from@example.test",
				keyMailFromName:    "Mailgun",
			},
			activity: `{"fields":["domain","endpoint","from_address","from_name","secret"],"group":"mail_mailgun"}`,
		},
		{
			group: groupMailResend,
			body: map[string]any{
				keyMailAPIKey:      mailResendFixture,
				keyMailFromAddress: "resend-from@example.test",
				keyMailFromName:    "Resend",
			},
			secrets:  []string{keyMailAPIKey},
			fixtures: []string{mailResendFixture},
			public: map[string]string{
				keyMailFromAddress: "resend-from@example.test",
				keyMailFromName:    "Resend",
			},
			activity: `{"fields":["api_key","from_address","from_name"],"group":"mail_resend"}`,
		},
		{
			group: groupMailPostmark,
			body: map[string]any{
				keyMailToken:         mailPostmarkFixture,
				keyMailMessageStream: "outbound",
				keyMailFromAddress:   "postmark-from@example.test",
				keyMailFromName:      "Postmark",
			},
			secrets:  []string{keyMailToken},
			fixtures: []string{mailPostmarkFixture},
			public: map[string]string{
				keyMailMessageStream: "outbound",
				keyMailFromAddress:   "postmark-from@example.test",
				keyMailFromName:      "Postmark",
			},
			activity: `{"fields":["from_address","from_name","message_stream_id","token"],"group":"mail_postmark"}`,
		},
	}
}

func TestSavePlatformMailProviders_SealsSecretsAndHidesThem(t *testing.T) {
	t.Parallel()

	for _, provider := range mailProviderCases() {
		t.Run(provider.group, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore()
			activity := &recordingActivity{}
			actor := uuid.New()
			service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
				WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
			ctx := context.Background()

			view, err := service.SavePlatform(ctx, actor, provider.group, provider.body)
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			assertMailProviderViewHidesSecrets(t, view, provider)
			for _, key := range provider.secrets {
				stored, ok := store.rows[platformStoreKey(provider.group)][key]
				if !ok || !IsSealed(stored) {
					t.Fatal("the secret was not sealed")
				}
			}
			for key, want := range provider.public {
				if store.rows[platformStoreKey(provider.group)][key] != want {
					t.Fatalf("stored %s = %q", key, store.rows[platformStoreKey(provider.group)][key])
				}
			}
			if len(activity.rows) != 1 {
				t.Fatalf("activity rows = %d", len(activity.rows))
			}
			meta, err := activity.rows[0].Metadata.Encode()
			if err != nil {
				t.Fatalf("metadata: %v", err)
			}
			if activityIncludesSecret(meta, provider.fixtures) {
				t.Fatal("activity metadata included a mail secret")
			}
			if meta != provider.activity || activity.rows[0].Action != "settings.updated" || activity.rows[0].TargetID != provider.group {
				t.Fatalf("activity metadata = %s", meta)
			}

			blank := map[string]any{}
			for _, key := range provider.secrets {
				blank[key] = ""
			}
			if _, err := service.SavePlatform(ctx, actor, provider.group, blank); err != nil {
				t.Fatalf("blank secret: %v", err)
			}
			for _, key := range provider.secrets {
				if store.rows[platformStoreKey(provider.group)][key] == "" || !IsSealed(store.rows[platformStoreKey(provider.group)][key]) {
					t.Fatal("a blank secret wiped the stored secret")
				}
			}
			if len(activity.rows) != 1 {
				t.Fatal("a blank secret was recorded as a change")
			}

			stranger := uuid.New()
			_, err = service.SavePlatform(ctx, stranger, provider.group, provider.body)
			if !errors.Is(err, ErrPlatformForbidden) {
				t.Fatalf("non-admin = %v", err)
			}
		})
	}
}

func TestSavePlatformMailSES_RequiresTheKeyPairTogether(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	_, err := service.SavePlatform(context.Background(), actor, groupMailSES, map[string]any{
		keyMailProviderKey: mailSESKeyFixture,
		keyMailRegion:      "us-east-1",
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyMailProviderSecret]) == 0 {
		t.Fatalf("half pair = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupMailSES)]; stored {
		t.Fatal("a half SES pair was stored")
	}

	view, err := service.SavePlatform(context.Background(), actor, groupMailSES, map[string]any{
		keyMailRegion: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("region only: %v", err)
	}
	if store.rows[platformStoreKey(groupMailSES)][keyMailRegion] != "eu-west-1" {
		t.Fatal("region was not stored")
	}
	if _, sealed := store.rows[platformStoreKey(groupMailSES)][keyMailProviderKey]; sealed {
		t.Fatal("region-only stored a secret")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), mailSESKeyFixture) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the response included a mail secret")
	}
}

func TestSaveMailProvidersIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	for _, name := range []string{groupMailSES, groupMailMailgun, groupMailResend, groupMailPostmark} {
		_, err := newTestService(newMemoryStore()).Save(
			context.Background(), uuid.New(), uuid.New(), "owner", name,
			map[string]any{keyMailFromName: "Macro"},
		)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s error = %v, want group not found", name, err)
		}
	}
}

func assertMailProviderViewHidesSecrets(t *testing.T, view GroupView, provider mailProviderCase) {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if activityIncludesSecret(string(encoded), provider.fixtures) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the response included a mail secret")
	}
	seen := map[string]Field{}
	for _, field := range view.Fields {
		seen[field.Key] = field
	}
	for _, key := range provider.secrets {
		field, ok := seen[key]
		if !ok || !field.Secret || !field.IsSet || field.Value != nil {
			t.Fatalf("secret field %s was not write-only", key)
		}
	}
	for key, want := range provider.public {
		field, ok := seen[key]
		if !ok || field.Secret || field.Value != want {
			t.Fatalf("public field %s = %#v", key, field.Value)
		}
	}
}

func activityIncludesSecret(meta string, fixtures []string) bool {
	if strings.Contains(meta, "enc:v1:") {
		return true
	}
	for _, fixture := range fixtures {
		if fixture != "" && strings.Contains(meta, fixture) {
			return true
		}
	}
	return false
}
