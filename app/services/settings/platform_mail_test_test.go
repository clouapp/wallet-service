package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestAuthorizePlatformMailTestAllowsAnAdminAndWritesNothing(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &countingMailTestActivity{}
	store := newMemoryStore()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	if err := service.AuthorizePlatformMailTest(context.Background(), actor); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
	if len(store.rows) != 0 {
		t.Fatalf("settings rows = %d", len(store.rows))
	}
}

func TestSendPlatformMailTestUsesTheSenderAndDropsTheTransportError(t *testing.T) {
	t.Parallel()

	activity := &countingMailTestActivity{}
	sender := &recordingTestMailer{err: errors.New("dial smtp.mail-test.invalid password mail-test-smtp-secret")}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformTestMailer(sender)

	err := service.SendPlatformMailTest(context.Background(), "mail-test@example.test")
	if !errors.Is(err, errPlatformTestMail) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "smtp.mail-test.invalid") || strings.Contains(err.Error(), "mail-test-smtp-secret") {
		t.Fatalf("transport detail leaked: %v", err)
	}
	if sender.n != 1 || sender.to != "mail-test@example.test" {
		t.Fatalf("sender = %+v", sender)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
}

func TestSendPlatformMailTestRefusesAMissingSender(t *testing.T) {
	t.Parallel()

	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: &countingMailTestActivity{}})
	err := service.SendPlatformMailTest(context.Background(), "mail-test@example.test")
	if err == nil {
		t.Fatal("expected a missing sender to fail")
	}
}

func TestAuthorizePlatformMailTestRefusesANonAdmin(t *testing.T) {
	t.Parallel()

	activity := &countingMailTestActivity{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{})

	err := service.AuthorizePlatformMailTest(context.Background(), uuid.New())
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("err = %v", err)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
}

type recordingTestMailer struct {
	to  string
	err error
	n   int
}

func (r *recordingTestMailer) Send(_ context.Context, to string) error {
	r.n++
	r.to = to
	return r.err
}

type countingMailTestActivity struct {
	n int
}

func (c *countingMailTestActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (c *countingMailTestActivity) Append(context.Context, models.AccountActivity) error {
	c.n++
	return nil
}
