package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/credentialmail"
)

func TestCredentialMailArgsCarryNoCredential(t *testing.T) {
	subjectID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	for _, purpose := range []string{credentialmail.PurposePasswordReset, credentialmail.PurposeAccountInvite} {
		args, err := CredentialMailArgs(subjectID, purpose)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 1 || args[0].Type != "string" {
			t.Fatalf("args = %#v", args)
		}
		text, ok := args[0].Value.(string)
		if !ok {
			t.Fatalf("arg %#v is not text", args[0].Value)
		}
		var payload credentialMailPayload
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.SubjectID != subjectID || payload.Purpose != purpose {
			t.Fatalf("payload = %#v", payload)
		}
		if strings.Contains(text, "token") || strings.Contains(text, "http") || strings.Contains(text, "@") {
			t.Fatalf("payload %q carries a credential", text)
		}
	}
	if _, err := CredentialMailArgs(uuid.Nil, credentialmail.PurposePasswordReset); err == nil {
		t.Fatal("nil subject must be refused")
	}
	if _, err := CredentialMailArgs(subjectID, "https://app.example/accept-invite?token=secret"); err == nil {
		t.Fatal("a link must not be a purpose")
	}
	if _, err := CredentialMailArgs(subjectID, credentialmail.PurposeWelcome); err == nil {
		t.Fatal("welcome must not be a queue purpose")
	}
}

func TestSendCredentialMailRejectsExtraArgsBeforeSending(t *testing.T) {
	job := &SendCredentialMailJob{}
	if job.Signature() != "send_credential_mail" {
		t.Fatalf("signature = %s", job.Signature())
	}
	if err := job.Handle(); err == nil {
		t.Fatal("empty args must be refused")
	}
	if err := job.Handle(uuid.New().String(), credentialmail.PurposePasswordReset); err == nil {
		t.Fatal("positional args must be refused")
	}
	if err := job.Handle(`{"subject_id":"` + uuid.New().String() + `","purpose":"` + credentialmail.PurposePasswordReset + `","token":"raw-token"}`); err == nil {
		t.Fatal("an extra field must be refused")
	}
	if err := job.Handle(`{"subject_id":"not-a-uuid","purpose":"` + credentialmail.PurposeAccountInvite + `"}`); err == nil {
		t.Fatal("invalid subject must be refused")
	}
	retry, delay := job.ShouldRetry(nil, 1)
	if retry || delay != 0 {
		t.Fatalf("retry = %v delay = %s", retry, delay)
	}
}

func TestSendCredentialMailHandleCallsSendOnce(t *testing.T) {
	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	var resets int
	job := NewSendCredentialMailJob(credentialmail.NewService(credentialmail.Deps{
		Users: mailUsers(func(id uuid.UUID) (*models.User, error) {
			if id != userID {
				t.Fatalf("lookup id = %s", id)
			}
			return &models.User{ID: userID, Email: "person@example.com", FullName: "Ada"}, nil
		}),
		Tokens:         mailTokens{raw: "reset-raw-token", hash: "reset-hash"},
		Resets:         mailResets{},
		Invites:        mailInvites{},
		Sender:         mailSender{reset: func() { resets++ }},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("handle must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("handle must not dispatch"); return "", nil },
	}))
	if err := job.Handle(`{"subject_id":"` + userID.String() + `","purpose":"` + credentialmail.PurposeWelcome + `"}`); err == nil {
		t.Fatal("welcome must not run on the credential job")
	}
	args, err := CredentialMailArgs(userID, credentialmail.PurposePasswordReset)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.Handle(args[0].Value); err != nil {
		t.Fatal(err)
	}
	if resets != 1 {
		t.Fatalf("reset sends = %d, want 1", resets)
	}
}

type mailUsers func(uuid.UUID) (*models.User, error)

func (f mailUsers) FindByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	return f(id)
}

type mailTokens struct {
	raw  string
	hash string
}

func (t mailTokens) GenerateRandomToken() (string, error) { return t.raw, nil }
func (t mailTokens) HashToken(string) string              { return t.hash }

type mailResets struct{}

func (mailResets) Create(context.Context, *models.PasswordResetToken) error { return nil }

type mailInvites struct{}

func (mailInvites) RefreshInviteForMail(context.Context, uuid.UUID, string) (account.InviteMail, error) {
	return account.InviteMail{}, nil
}

type mailSender struct{ reset func() }

func (mailSender) SendInvite(context.Context, account.InviteMail) error { return nil }
func (s mailSender) SendReset(context.Context, string, string) error {
	if s.reset != nil {
		s.reset()
	}
	return nil
}
func (mailSender) SendWelcome(context.Context, string, string) error { return nil }
