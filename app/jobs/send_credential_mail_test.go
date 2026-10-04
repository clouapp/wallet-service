package jobs

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/credentialmail"
)

func TestCredentialMailArgsCarryNoCredential(t *testing.T) {
	subjectID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	for _, purpose := range []string{credentialmail.PurposePasswordReset, credentialmail.PurposeAccountInvite} {
		args, err := CredentialMailArgs(subjectID, purpose)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 2 {
			t.Fatalf("args = %#v", args)
		}
		if args[0].Value != subjectID.String() || args[1].Value != purpose {
			t.Fatalf("args = %#v", args)
		}
		for _, arg := range args {
			text, ok := arg.Value.(string)
			if !ok {
				t.Fatalf("arg %#v is not text", arg.Value)
			}
			if strings.Contains(text, "token") || strings.Contains(text, "http") || strings.Contains(text, "@") {
				t.Fatalf("payload %q carries a credential", text)
			}
		}
	}
	if _, err := CredentialMailArgs(uuid.Nil, credentialmail.PurposePasswordReset); err == nil {
		t.Fatal("nil subject must be refused")
	}
	if _, err := CredentialMailArgs(subjectID, "https://app.example/accept-invite?token=secret"); err == nil {
		t.Fatal("a link must not be a purpose")
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
	if err := job.Handle(uuid.New().String(), credentialmail.PurposePasswordReset, "raw-token"); err == nil {
		t.Fatal("a third argument must be refused")
	}
	if err := job.Handle("not-a-uuid", credentialmail.PurposeAccountInvite); err == nil {
		t.Fatal("invalid subject must be refused")
	}
	retry, delay := job.ShouldRetry(nil, 1)
	if retry || delay != 0 {
		t.Fatalf("retry = %v delay = %s", retry, delay)
	}
}
