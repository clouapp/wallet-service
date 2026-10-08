package jobs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/credentialmail"
)

func TestCredential_Mail_DispatcherPayloadIsSubjectAndPurpose(t *testing.T) {
	fake := &mailQueue{}
	dispatcher := NewCredentialMailDispatcher(func() Enqueuer { return fake }, noMailService)
	userID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	inviteID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	const secret = "raw-token-must-stay-off-the-queue"

	if err := dispatcher.DispatchPasswordReset(userID); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DispatchAccountInvite(inviteID); err != nil {
		t.Fatal(err)
	}
	if len(fake.payloads) != 2 {
		t.Fatalf("payloads = %d", len(fake.payloads))
	}
	assertMailPayload(t, fake.payloads[0], userID, credentialmail.PurposePasswordReset, secret)
	assertMailPayload(t, fake.payloads[1], inviteID, credentialmail.PurposeAccountInvite, secret)
}

func TestCredential_Mail_DispatcherRequiresAQueueClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for a nil queue client")
		}
	}()
	NewCredentialMailDispatcher(nil, noMailService)
}

func TestCredential_Mail_DispatcherRequiresAMailService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for a nil mail service source")
		}
	}()
	NewCredentialMailDispatcher(func() Enqueuer { return &mailQueue{} }, nil)
}

// The sync driver runs Handle on the job it is given, so that job must carry
// the mail service the composition root resolved.
func TestCredential_Mail_DispatcherHandsTheJobTheMailService(t *testing.T) {
	fake := &mailQueue{}
	mailer := idleMailService()
	dispatcher := NewCredentialMailDispatcher(func() Enqueuer { return fake }, func() (*credentialmail.Service, error) {
		return mailer, nil
	})

	if err := dispatcher.DispatchPasswordReset(uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DispatchAccountInvite(uuid.New()); err != nil {
		t.Fatal(err)
	}
	if len(fake.services) != 2 || fake.services[0] != mailer || fake.services[1] != mailer {
		t.Fatalf("jobs carried %v, want the resolved mail service twice", fake.services)
	}
}

func TestCredential_Mail_DispatcherRefusesWhenTheMailServiceDoesNotResolve(t *testing.T) {
	fake := &mailQueue{}
	unresolved := errString("mail service is not bound")
	dispatcher := NewCredentialMailDispatcher(func() Enqueuer { return fake }, func() (*credentialmail.Service, error) {
		return nil, unresolved
	})

	if err := dispatcher.DispatchPasswordReset(uuid.New()); err != unresolved {
		t.Fatalf("reset error = %v, want %v", err, unresolved)
	}
	if _, err := dispatcher.DispatchAccountInvite(uuid.New()); err != unresolved {
		t.Fatalf("invite error = %v, want %v", err, unresolved)
	}
	if len(fake.payloads) != 0 {
		t.Fatal("a dispatch without a mail service reached the queue")
	}
}

// noMailService stands for a mail service the payload tests never reach: the
// fake queue records the payload and does not run the job.
func noMailService() (*credentialmail.Service, error) {
	return idleMailService(), nil
}

func idleMailService() *credentialmail.Service {
	return credentialmail.NewService(credentialmail.Deps{
		Users:          mailUsers(func(uuid.UUID) (*models.User, error) { return nil, nil }),
		Tokens:         mailTokens{},
		Resets:         mailResets{},
		Invites:        mailInvites{},
		Sender:         mailSender{},
		Dispatch:       func(uuid.UUID, string) error { return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { return "", nil },
	})
}

func assertMailPayload(t *testing.T, payload string, subjectID uuid.UUID, purpose, secret string) {
	t.Helper()
	var decoded struct {
		SubjectID uuid.UUID `json:"subject_id"`
		Purpose   string    `json:"purpose"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SubjectID != subjectID || decoded.Purpose != purpose {
		t.Fatalf("payload = %s", payload)
	}
	if strings.Contains(payload, secret) || strings.Contains(payload, "token") || strings.Contains(payload, "link") {
		t.Fatalf("payload carried a credential: %s", payload)
	}
}

type mailQueue struct {
	payloads []string
	services []*credentialmail.Service
}

func (f *mailQueue) Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob {
	if sent, ok := job.(*SendCredentialMailJob); ok {
		f.services = append(f.services, sent.service)
	}
	return &mailPending{queue: f, signature: job.Signature(), args: args}
}

type mailPending struct {
	queue     *mailQueue
	signature string
	args      [][]queue.Arg
}

func (p *mailPending) Delay(time.Time) queue.PendingJob { return p }

func (p *mailPending) Dispatch() error { return p.record() }

func (p *mailPending) DispatchSync() error { return p.record() }

func (p *mailPending) OnConnection(string) queue.PendingJob { return p }

func (p *mailPending) OnQueue(string) queue.PendingJob { return p }

func (p *mailPending) record() error {
	if p.signature != "send_credential_mail" {
		return errMailSignature
	}
	if len(p.args) != 1 || len(p.args[0]) != 1 {
		return errMailArgs
	}
	payload, ok := p.args[0][0].Value.(string)
	if !ok {
		return errMailArgs
	}
	p.queue.payloads = append(p.queue.payloads, payload)
	return nil
}

var (
	errMailSignature = errString("send_credential_mail: signature")
	errMailArgs      = errString("send_credential_mail: args")
)

type errString string

func (e errString) Error() string { return string(e) }
