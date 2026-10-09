package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/credentialmail"
)

func TestQueued_Credential_MailHasNoCredentialAndMailQueueRefuses(t *testing.T) {
	const (
		inviteIDRaw = "22222222-2222-2222-2222-222222222222"
		mintedLink  = "https://app.example/accept-invite?token=invite-raw-token"
		password    = "correct-horse-battery-staple"
	)
	inviteID := uuid.MustParse(inviteIDRaw)
	t.Setenv("APP_FRONTEND_URL", "https://app.example")

	mailer := credentialmail.NewService(credentialmail.Deps{
		Users:  queueUserLookup{},
		Tokens: queueTokenIssuer{},
		Resets: queueResetWriter{},
		Invites: queueInviteRefresher{mail: account.InviteMail{
			To: "new@example.com", InvitedBy: "Ada", AccountName: "Org", Link: mintedLink,
		}},
		Sender:         queueSender{},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("the job must not enqueue another mail"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("the job must not dispatch itself"); return "", nil },
	})

	var payload string
	got, err := jobs.DispatchSyncAccountInvite(func(job queue.Job, args []queue.Arg) error {
		encoded, marshalErr := json.Marshal(args)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		payload = string(encoded)
		values := make([]any, 0, len(args))
		for _, arg := range args {
			values = append(values, arg.Value)
		}
		return job.Handle(values...)
	}, inviteID, mailer)
	if err != nil {
		t.Fatal(err)
	}
	if got != mintedLink {
		t.Fatal("sync invite did not return the minted link")
	}
	assertQueuedMessageHasNoCredential(t, payload, inviteIDRaw, credentialmail.PurposeAccountInvite, mintedLink, password)

	resetID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	resetArgs, err := jobs.CredentialMailArgs(resetID, credentialmail.PurposePasswordReset)
	if err != nil {
		t.Fatal(err)
	}
	resetEncoded, err := json.Marshal(resetArgs)
	if err != nil {
		t.Fatal(err)
	}
	assertQueuedMessageHasNoCredential(t, string(resetEncoded), resetID.String(), credentialmail.PurposePasswordReset, mintedLink, password)

	if err := NewMailer(MailerDeps{}).Queue(); !errors.Is(err, errQueueRefused) {
		t.Fatalf("Queue() error = %v", err)
	}
}

func assertQueuedMessageHasNoCredential(t *testing.T, payload, subjectID, purpose, link, password string) {
	t.Helper()
	wantArgs, err := jobs.CredentialMailArgs(uuid.MustParse(subjectID), purpose)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(wantArgs)
	if err != nil {
		t.Fatal(err)
	}
	if payload != string(want) {
		t.Fatal("queued message is not the subject id and purpose")
	}
	stripped := strings.ReplaceAll(payload, purpose, "")
	if strings.Contains(payload, link) || strings.Contains(payload, password) ||
		strings.Contains(stripped, "password") || strings.Contains(stripped, "token") || strings.Contains(stripped, "http") {
		t.Fatal("queued message carries a credential")
	}
}

type queueUserLookup struct{}

func (queueUserLookup) FindByID(context.Context, uuid.UUID) (*models.User, error) { return nil, nil }

type queueTokenIssuer struct{}

func (queueTokenIssuer) GenerateRandomToken() (string, error) { return "", nil }
func (queueTokenIssuer) HashToken(string) string              { return "" }

type queueResetWriter struct{}

func (queueResetWriter) Create(context.Context, *models.PasswordResetToken) error { return nil }

type queueInviteRefresher struct {
	mail account.InviteMail
}

func (s queueInviteRefresher) RefreshInviteForMail(context.Context, uuid.UUID, string) (account.InviteMail, error) {
	return s.mail, nil
}

type queueSender struct{}

func (queueSender) SendInvite(context.Context, account.InviteMail) error { return nil }
func (queueSender) SendReset(context.Context, string, string) error      { return nil }
func (queueSender) SendWelcome(context.Context, string, string) error    { return nil }
