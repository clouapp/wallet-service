package users_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/users"
)

func TestRequest_Password_ResetDispatchesOnlyTheUserID(t *testing.T) {
	userID := uuid.New()
	var got uuid.UUID
	store := &resetMailStore{user: &models.User{ID: userID, Email: "ada@example.com"}}
	svc := users.NewService(users.Deps{
		Store: store,
		ResetMail: resetMailFunc(func(id uuid.UUID) error {
			got = id
			return nil
		}),
	})

	if err := svc.RequestPasswordReset(context.Background(), "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if got != userID {
		t.Fatalf("dispatched %s", got)
	}
	if store.email != "ada@example.com" {
		t.Fatalf("lookup email = %q", store.email)
	}
}

func TestRequest_Password_ResetSkipsAMissingUser(t *testing.T) {
	store := &resetMailStore{err: errors.New("db down")}
	svc := users.NewService(users.Deps{
		Store: store,
		ResetMail: resetMailFunc(func(uuid.UUID) error {
			t.Fatal("lookup failure must not dispatch")
			return nil
		}),
	})
	if err := svc.RequestPasswordReset(context.Background(), "missing@example.com"); err != nil {
		t.Fatal(err)
	}

	store.err = nil
	if err := svc.RequestPasswordReset(context.Background(), "missing@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestRequest_Password_ResetRequiresTheDispatcherWhenTheUserExists(t *testing.T) {
	svc := users.NewService(users.Deps{
		Store: &resetMailStore{user: &models.User{ID: uuid.New(), Email: "ada@example.com"}},
	})
	if err := svc.RequestPasswordReset(context.Background(), "ada@example.com"); err == nil {
		t.Fatal("missing dispatcher was accepted")
	}
}

type resetMailFunc func(uuid.UUID) error

func (f resetMailFunc) DispatchPasswordReset(userID uuid.UUID) error { return f(userID) }

type resetMailStore struct {
	user  *models.User
	email string
	err   error
}

func (f *resetMailStore) FindByEmail(_ context.Context, email string) (*models.User, error) {
	f.email = email
	if f.err != nil {
		return nil, f.err
	}
	return f.user, nil
}

func (f *resetMailStore) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	return nil, errors.New("unused")
}
func (f *resetMailStore) Create(context.Context, *models.User) error { return errors.New("unused") }
func (f *resetMailStore) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return errors.New("unused")
}
func (f *resetMailStore) UpdateFullName(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (f *resetMailStore) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (f *resetMailStore) UpdatePreferences(context.Context, uuid.UUID, *models.UserPreferences) error {
	return errors.New("unused")
}
func (f *resetMailStore) UpdateTotpSecret(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (f *resetMailStore) EnableTotp(context.Context, uuid.UUID) error { return errors.New("unused") }
func (f *resetMailStore) DisableTotp(context.Context, uuid.UUID) error {
	return errors.New("unused")
}
func (f *resetMailStore) SetSuspendedAt(context.Context, uuid.UUID, *time.Time) error {
	return errors.New("unused")
}
func (f *resetMailStore) List(context.Context, int, int) ([]models.User, int64, error) {
	return nil, 0, errors.New("unused")
}

func TestForgot_Password_DispatchesAndLogsAFailureItDoesNotReturn(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	dispatched := 0
	svc := users.NewService(users.Deps{
		Store: &resetMailStore{user: &models.User{ID: uuid.New(), Email: "ada@example.com"}},
		ResetMail: resetMailFunc(func(uuid.UUID) error {
			dispatched++
			return errors.New("queue down")
		}),
	})

	svc.ForgotPassword(context.Background(), "ada@example.com")

	if dispatched != 1 {
		t.Fatalf("dispatches = %d, want 1", dispatched)
	}
	if !strings.Contains(logs.String(), "auth: send password reset mail failed") {
		t.Fatal("the failed dispatch was not logged")
	}
	if strings.Contains(logs.String(), "ada@example.com") {
		t.Fatal("the log carries the address")
	}
}
