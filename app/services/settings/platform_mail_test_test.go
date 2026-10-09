package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestSend_Platform_MailTestSendsForAnAdminAndWritesNothing(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &countingMailTestActivity{}
	store := newMemoryStore()
	sender := &recordingTestMailer{}
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithPlatformTestMailer(sender)

	if err := service.SendPlatformMailTest(context.Background(), actor, recipient("mail-test@example.test")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if sender.n != 1 || sender.to != "mail-test@example.test" {
		t.Fatalf("sender = %+v", sender)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
	if len(store.rows) != 0 {
		t.Fatalf("settings rows = %d", len(store.rows))
	}
}

func TestSend_Platform_MailTestUsesTheSenderAndDropsTheTransportError(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	activity := &countingMailTestActivity{}
	sender := &recordingTestMailer{err: errors.New("dial smtp.mail-test.invalid password mail-test-smtp-secret")}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithPlatformTestMailer(sender)

	err := service.SendPlatformMailTest(context.Background(), actor, recipient("mail-test@example.test"))
	if !errors.Is(err, ErrPlatformTestMail) {
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

func TestSend_Platform_MailTestRefusesAMissingSender(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: &countingMailTestActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	err := service.SendPlatformMailTest(context.Background(), actor, recipient("mail-test@example.test"))
	if !errors.Is(err, ErrPlatformTestMail) {
		t.Fatalf("a missing sender is a failed send, got %v", err)
	}
}

func TestSend_Platform_MailTestRefusesANonAdminBeforeReadingTheRecipient(t *testing.T) {
	t.Parallel()

	activity := &countingMailTestActivity{}
	sender := &recordingTestMailer{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{}).
		WithPlatformTestMailer(sender)
	to := recipient("mail-test@example.test")

	err := service.SendPlatformMailTest(context.Background(), uuid.New(), to)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("err = %v", err)
	}
	if to.reads != 0 || sender.n != 0 {
		t.Fatalf("a non-admin read the recipient %d times and sent %d", to.reads, sender.n)
	}
	if activity.n != 0 {
		t.Fatalf("activity rows = %d", activity.n)
	}
}

// TestSend_Platform_MailTestReturnsARecipientRefusalAsItIs pins the one call
// the handler makes: an admin's recipient is read after the check, and a
// recipient that cannot be read is that error, untouched, with nothing sent.
func TestSend_Platform_MailTestReturnsARecipientRefusalAsItIs(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	sender := &recordingTestMailer{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: prefixSealer{}, Cache: nopCache{}, Activity: &countingMailTestActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithPlatformTestMailer(sender)
	refused := errors.New("validation failed")
	to := &recordedRecipient{err: refused}

	err := service.SendPlatformMailTest(context.Background(), actor, to)

	if err != refused {
		t.Fatalf("err = %v, want the refusal as it is", err)
	}
	if to.reads != 1 || sender.n != 0 {
		t.Fatalf("recipient reads = %d, sends = %d", to.reads, sender.n)
	}
}

// recordedRecipient is the mail test address, counting its reads.
type recordedRecipient struct {
	to    string
	err   error
	reads int
}

func recipient(to string) *recordedRecipient { return &recordedRecipient{to: to} }

func (r *recordedRecipient) Read() (string, error) {
	r.reads++
	return r.to, r.err
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
