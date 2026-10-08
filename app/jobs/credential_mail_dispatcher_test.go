package jobs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/services/credentialmail"
)

func TestCredential_Mail_DispatcherPayloadIsSubjectAndPurpose(t *testing.T) {
	fake := &mailQueue{}
	dispatcher := NewCredentialMailDispatcher(func() Enqueuer { return fake })
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
	NewCredentialMailDispatcher(nil)
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
}

func (f *mailQueue) Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob {
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
