package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const mailSMTPFixture = "mailbox-secret-value"

func TestEffectiveMailSMTP_MissingRowKeepsEveryFieldUnused(t *testing.T) {
	t.Parallel()

	got, err := newTestService(newMemoryStore()).EffectiveMailSMTP(context.Background())
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got != (MailSMTP{}) {
		t.Fatalf("effective = %+v, want the env mailer", mailSMTPForFailure(got))
	}
}

func TestEffectiveMailSMTP_OpensASealedPasswordAndSkipsABadField(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailHost:       "127.0.0.1",
		keyMailPort:       "2525",
		keyMailEncryption: mailEncryptionStartTLS,
		keyMailUsername:   "mailer",
		keyMailPassword:   "enc:v1:" + mailSMTPFixture,
	})
	got, err := newTestService(store).EffectiveMailSMTP(context.Background())
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if !got.UseHost || got.Host != "127.0.0.1" || !got.UsePort || got.Port != 2525 ||
		!got.UseEncryption || got.Encryption != mailEncryptionStartTLS || !got.UseUsername || got.Username != "mailer" ||
		!got.UsePassword || got.Password != mailSMTPFixture {
		t.Fatal("sealed mail_smtp row was not opened for the mailer")
	}
	if strings.Contains(got.Password, "enc:v1:") {
		t.Fatal("the mailer received ciphertext")
	}

	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailHost:       "not a host",
		keyMailPort:       "0",
		keyMailEncryption: "ssl",
		keyMailUsername:   "mailer",
		keyMailPassword:   mailSMTPFixture,
	})
	invalid, err := newTestService(store).EffectiveMailSMTP(context.Background())
	if err != nil {
		t.Fatalf("invalid: %v", err)
	}
	if invalid.UseHost || invalid.UsePort || invalid.UseEncryption || invalid.UsePassword || !invalid.UseUsername {
		t.Fatal("an invalid mail_smtp field replaced the env mailer")
	}
	if invalid.Password != "" {
		t.Fatal("an unsealed password was returned")
	}
}

func TestEffectiveMailSMTP_StoreError(t *testing.T) {
	t.Parallel()

	_, err := newTestService(platformErrStore{err: errors.New("db down")}).EffectiveMailSMTP(context.Background())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v, want db down", err)
	}
}

func TestSavePlatformMailSMTP_SealsThePasswordAndHidesIt(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	view, err := service.SavePlatform(ctx, actor, groupMailSMTP, map[string]any{
		keyMailHost:       "127.0.0.1",
		keyMailPort:       2525,
		keyMailEncryption: mailEncryptionStartTLS,
		keyMailUsername:   "mailer",
		keyMailPassword:   mailSMTPFixture,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), mailSMTPFixture) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the response included the mail password")
	}
	var password Field
	for _, field := range view.Fields {
		if field.Key == keyMailPassword {
			password = field
		}
		if field.Key == keyMailHost && field.Value != "127.0.0.1" {
			t.Fatalf("host = %#v", field.Value)
		}
	}
	if !password.Secret || !password.IsSet || password.Value != nil {
		t.Fatal("the password field was not write-only")
	}
	stored, ok := store.rows[platformStoreKey(groupMailSMTP)][keyMailPassword]
	if !ok || stored != "enc:v1:"+mailSMTPFixture || strings.Contains(stored, mailSMTPFixture) && !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatal("the password was not sealed")
	}
	if strings.Contains(stored, mailSMTPFixture) && !IsSealed(stored) {
		t.Fatal("the password was stored in the clear")
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d", len(activity.rows))
	}
	meta, err := activity.rows[0].Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	const want = `{"fields":["encryption","host","password","port","username"],"group":"mail_smtp"}`
	if meta != want || strings.Contains(meta, mailSMTPFixture) || strings.Contains(meta, "enc:v1:") {
		t.Fatal("activity metadata included a mail value")
	}

	if _, err := service.SavePlatform(ctx, actor, groupMailSMTP, map[string]any{keyMailPassword: ""}); err != nil {
		t.Fatalf("blank password: %v", err)
	}
	kept := store.rows[platformStoreKey(groupMailSMTP)][keyMailPassword]
	if kept != stored {
		t.Fatal("a blank password wiped the stored secret")
	}
	if len(activity.rows) != 1 {
		t.Fatal("a blank password was recorded as a change")
	}

	_, err = service.SavePlatform(ctx, actor, groupMailSMTP, map[string]any{keyMailHost: "127.0.0.2"})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyMailPassword]) == 0 {
		t.Fatalf("destination change = %v", err)
	}
	if store.rows[platformStoreKey(groupMailSMTP)][keyMailHost] != "127.0.0.1" {
		t.Fatal("a destination change without the password was stored")
	}

	stranger := uuid.New()
	_, err = service.SavePlatform(ctx, stranger, groupMailSMTP, map[string]any{keyMailPort: 2525})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
}

func TestSaveMailSMTPIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupMailSMTP,
		map[string]any{keyMailPort: 2525},
	)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("error = %v, want group not found", err)
	}
}

func mailSMTPForFailure(got MailSMTP) MailSMTP {
	got.Password = ""
	return got
}
