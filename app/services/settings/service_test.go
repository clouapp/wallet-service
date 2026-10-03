package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memoryStore struct {
	rows map[string]map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[string]map[string]string{}}
}

func (s *memoryStore) key(accountID uuid.UUID, group string) string {
	return accountID.String() + "\x00" + group
}

func (s *memoryStore) ListGroup(_ context.Context, accountID uuid.UUID, group string) ([]models.Setting, error) {
	values := s.rows[s.key(accountID, group)]
	rows := make([]models.Setting, 0, len(values))
	id := accountID
	for key, value := range values {
		rows = append(rows, models.Setting{AccountID: &id, Group: group, Key: key, Value: value})
	}
	return rows, nil
}

func (s *memoryStore) UpsertMany(_ context.Context, accountID uuid.UUID, group string, values map[string]string) error {
	bucket := s.rows[s.key(accountID, group)]
	if bucket == nil {
		bucket = map[string]string{}
		s.rows[s.key(accountID, group)] = bucket
	}
	for key, value := range values {
		bucket[key] = value
	}
	return nil
}

func (s *memoryStore) get(accountID uuid.UUID, group, key string) (string, bool) {
	value, ok := s.rows[s.key(accountID, group)][key]
	return value, ok
}

type prefixSealer struct{}

func (prefixSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + plaintext, nil
}

type discardActivity struct{}

func (discardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (discardActivity) Append(context.Context, models.AccountActivity) error { return nil }

func newTestService(store Store) *Service {
	return NewService(store, prefixSealer{}, nopCache{}, discardActivity{})
}

func TestSaveBlankSecretKeepsTheStoredCiphertext(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()

	if _, err := service.Save(ctx, accountID, uuid.New(), "owner", groupAccountWebhooks, map[string]any{
		keySigningSecret: "first-secret",
	}); err != nil {
		t.Fatalf("save secret: %v", err)
	}
	before, ok := store.get(accountID, groupAccountWebhooks, keySigningSecret)
	if !ok || before != "enc:v1:first-secret" {
		t.Fatalf("stored secret = %q, present %v", before, ok)
	}

	if _, err := service.Save(ctx, accountID, uuid.New(), "admin", groupAccountWebhooks, map[string]any{
		keySigningSecret: "",
	}); err != nil {
		t.Fatalf("save blank secret: %v", err)
	}
	after, _ := store.get(accountID, groupAccountWebhooks, keySigningSecret)
	if after != before {
		t.Fatalf("blank secret replaced %q with %q", before, after)
	}

	view, err := service.Save(ctx, accountID, uuid.New(), "owner", groupAccountWebhooks, map[string]any{
		keyDefaultEvents: []any{"deposit.confirmed", "withdrawal.confirmed"},
	})
	if err != nil {
		t.Fatalf("save without secret: %v", err)
	}
	kept, _ := store.get(accountID, groupAccountWebhooks, keySigningSecret)
	if kept != before {
		t.Fatalf("omitted secret replaced %q with %q", before, kept)
	}
	secret := fieldByKey(t, view, keySigningSecret)
	if !secret.IsSet || secret.Value != nil {
		t.Fatalf("secret field = %+v", secret)
	}
}

func TestSaveNewSecretIsStoredAndHidden(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()

	view, err := service.Save(context.Background(), accountID, uuid.New(), "owner", groupAccountWebhooks, map[string]any{
		keySigningSecret: "second-secret",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	stored, ok := store.get(accountID, groupAccountWebhooks, keySigningSecret)
	if !ok || stored != "enc:v1:second-secret" {
		t.Fatalf("stored = %q, present %v", stored, ok)
	}
	secret := fieldByKey(t, view, keySigningSecret)
	if !secret.Secret || !secret.IsSet || secret.Value != nil {
		t.Fatalf("response field = %+v", secret)
	}
}

func TestSaveRejectsAnUnknownGroupBeforeTheRoleCheck(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	_, err := service.Save(context.Background(), uuid.New(), uuid.New(), "auditor", "not-a-group", map[string]any{})
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveAuditorCannotUpdate(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	_, err := service.Save(context.Background(), uuid.New(), uuid.New(), "auditor", groupAccountWebhooks, map[string]any{
		keySigningSecret: "nope",
	})
	if !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveUnknownKeyIsValidation(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	_, err := service.Save(context.Background(), uuid.New(), uuid.New(), "owner", groupAccountSecurity, map[string]any{
		"not_a_key": "x",
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v", err)
	}
	if len(invalid.Fields["not_a_key"]) != 1 {
		t.Fatalf("fields = %#v", invalid.Fields)
	}
}

func TestRegistryHidesSecrets(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if _, err := service.Save(ctx, accountID, uuid.New(), "owner", groupAccountWebhooks, map[string]any{
		keySigningSecret: "hidden",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	view, err := service.Registry(ctx, accountID, "auditor")
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	var secret Field
	found := false
	for _, section := range view.Sections {
		for _, block := range section.Blocks {
			for _, group := range block.Groups {
				if group.Name != groupAccountWebhooks {
					continue
				}
				if group.CanUpdate {
					t.Fatal("auditor can_update is true")
				}
				secret = fieldByKey(t, group, keySigningSecret)
				found = true
			}
		}
	}
	if !found || !secret.IsSet || secret.Value != nil {
		t.Fatalf("secret field found=%v %+v", found, secret)
	}
	if _, err := service.Registry(ctx, accountID, "user"); !errors.Is(err, ErrViewForbidden) {
		t.Fatalf("user registry err = %v", err)
	}
}

func TestDecimalStaysAString(t *testing.T) {
	t.Parallel()

	stored, err := castIn("1.50", Definition{Type: TypeDecimal})
	if err != nil {
		t.Fatalf("castIn: %v", err)
	}
	if stored != "1.50" {
		t.Fatalf("stored = %q", stored)
	}
	if _, err := castIn(1.5, Definition{Type: TypeDecimal}); err == nil {
		t.Fatal("a JSON number was accepted as a decimal")
	}
	if castOut("1.50", Definition{Type: TypeDecimal}) != "1.50" {
		t.Fatal("decimal was not returned as a string")
	}
}

func fieldByKey(t *testing.T, group GroupView, key string) Field {
	t.Helper()
	for _, field := range group.Fields {
		if field.Key == key {
			return field
		}
	}
	t.Fatalf("field %s not in group %s", key, group.Name)
	return Field{}
}
