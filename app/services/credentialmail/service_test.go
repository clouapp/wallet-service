package credentialmail

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
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

func TestSend_Password_ResetStoresOnlyTheHash(t *testing.T) {
	userID := uuid.New()
	const raw = "reset-raw-token"
	var stored string
	var sentTo, sentLink string
	svc := NewService(Deps{
		Users: userLookupFunc(func(ctx context.Context, id uuid.UUID) (*models.User, error) {
			if id != userID {
				t.Fatalf("lookup id = %s", id)
			}
			return &models.User{ID: userID, Email: "person@example.com"}, nil
		}),
		Tokens: tokenIssuerFunc{
			mint: func() (string, error) { return raw, nil },
			hash: authsvc.NewService(nil).HashToken,
		},
		Resets: resetWriterFunc(func(ctx context.Context, token *models.PasswordResetToken) error {
			stored = token.TokenHash
			if token.UserID != userID {
				t.Fatalf("stored user = %s", token.UserID)
			}
			if token.ExpiresAt.Before(time.Now()) {
				t.Fatal("reset token is already expired")
			}
			return nil
		}),
		Invites:        inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:         senderFunc{reset: func(to, link string) { sentTo, sentLink = to, link }},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("send must not dispatch an invite"); return "", nil },
	})

	if err := svc.SendPasswordReset(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if stored == "" || stored == raw || strings.Contains(stored, raw) {
		t.Fatalf("stored hash leaked the token: %q", stored)
	}
	if !authsvc.NewService(nil).CheckToken(raw, stored) {
		t.Fatal("stored value is not the hash of the minted token")
	}
	if sentTo != "person@example.com" || !strings.Contains(sentLink, raw) || strings.Contains(sentLink, stored) {
		t.Fatalf("sent to %q link %q", sentTo, sentLink)
	}
}

func TestSend_Account_InviteDoesNotEnqueueTheLink(t *testing.T) {
	inviteID := uuid.New()
	const link = "https://app.example/accept-invite?token=invite-raw"
	var sent account.InviteMail
	svc := NewService(Deps{
		Users:  userLookupFunc(func(context.Context, uuid.UUID) (*models.User, error) { return nil, nil }),
		Tokens: tokenIssuerFunc{mint: func() (string, error) { return "", nil }, hash: func(string) string { return "" }},
		Resets: resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites: inviteRefresherFunc(func(_ context.Context, id uuid.UUID, base string) (account.InviteMail, error) {
			if id != inviteID || base == "" {
				t.Fatalf("refresh id %s base %q", id, base)
			}
			return account.InviteMail{To: "new@example.com", InvitedBy: "Ada", AccountName: "Org", Link: link}, nil
		}),
		Sender:         senderFunc{invite: func(message account.InviteMail) { sent = message }},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("invite send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("invite send must not dispatch"); return "", nil },
	})
	t.Setenv("APP_FRONTEND_URL", "https://app.example")

	got, err := svc.SendAccountInvite(context.Background(), inviteID)
	if err != nil {
		t.Fatal(err)
	}
	if got != link || sent.Link != link || sent.To != "new@example.com" {
		t.Fatalf("got %q sent %+v", got, sent)
	}
}

func TestSend_Runs_TheDecodedPurpose(t *testing.T) {
	userID := uuid.New()
	var welcome int
	svc := NewService(Deps{
		Users: userLookupFunc(func(_ context.Context, id uuid.UUID) (*models.User, error) {
			if id != userID {
				t.Fatalf("lookup id = %s", id)
			}
			return &models.User{ID: userID, Email: "person@example.com", FullName: "Ada"}, nil
		}),
		Tokens:         tokenIssuerFunc{mint: func() (string, error) { return "raw", nil }, hash: func(string) string { return "hash" }},
		Resets:         resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites:        inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:         senderFunc{welcome: func(string, string) { welcome++ }},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("send must not dispatch an invite"); return "", nil },
	})

	if _, err := svc.Send(context.Background(), userID, PurposeWelcome); err == nil || welcome != 0 {
		t.Fatalf("welcome send via the job purpose err %v welcome %d", err, welcome)
	}
	if _, err := svc.Send(context.Background(), userID, "not-a-purpose"); err == nil {
		t.Fatal("unknown purpose must be refused")
	}
}

func TestSend_Welcome_UsesTheLoadedUser(t *testing.T) {
	userID := uuid.New()
	const hash = "stored-password-hash"
	var sentTo, sentName string
	svc := NewService(Deps{
		Users: userLookupFunc(func(_ context.Context, id uuid.UUID) (*models.User, error) {
			if id != userID {
				t.Fatalf("lookup id = %s", id)
			}
			return &models.User{ID: userID, Email: "person@example.com", FullName: "Ada", PasswordHash: hash}, nil
		}),
		Tokens:         tokenIssuerFunc{mint: func() (string, error) { return "raw", nil }, hash: func(string) string { return hash }},
		Resets:         resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites:        inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:         senderFunc{welcome: func(to, fullName string) { sentTo, sentName = to, fullName }},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("welcome send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("welcome send must not dispatch an invite"); return "", nil },
	})

	if err := svc.SendWelcome(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if sentTo != "person@example.com" || sentName != "Ada" || strings.Contains(sentName, hash) || strings.Contains(sentTo, hash) {
		t.Fatalf("sent to %q name %q", sentTo, sentName)
	}
}

func TestDeadline_Mid_SendLogsWarnWithoutTheCredential(t *testing.T) {
	const raw = "super-secret-reset-token"
	const inviteLink = "https://app.example/accept-invite?token=invite-secret"
	userID := uuid.New()
	inviteID := uuid.New()
	var mints int

	logs := captureCredentialMailLogs(t)
	resetCtx := newMidSendDeadline()
	svc := credentialService(t, userID, func() (string, error) {
		mints++
		return raw, nil
	}, senderFunc{
		reset:    func(string, string) { resetCtx.hit() },
		resetErr: errors.New("smtp: " + raw),
	})

	err := svc.SendPasswordReset(resetCtx, userID)
	if err == nil || err.Error() != "password reset mail: send failed" {
		t.Fatalf("reset err = %v", err)
	}
	if mints != 1 {
		t.Fatalf("mints = %d", mints)
	}
	assertUnknownSendWarn(t, logs.String(), PurposePasswordReset, raw, "person@example.com")

	logs.Reset()
	mints = 0
	inviteCtx := newMidSendDeadline()
	var refreshes int
	svc = NewService(Deps{
		Users:  userLookupFunc(func(context.Context, uuid.UUID) (*models.User, error) { return nil, nil }),
		Tokens: tokenIssuerFunc{mint: func() (string, error) { mints++; return "", nil }, hash: func(string) string { return "" }},
		Resets: resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites: inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) {
			refreshes++
			return account.InviteMail{To: "new@example.com", Link: inviteLink}, nil
		}),
		Sender: senderFunc{
			invite:    func(account.InviteMail) { inviteCtx.hit() },
			inviteErr: errors.New("smtp: " + inviteLink),
		},
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("send must not dispatch"); return "", nil },
	})
	t.Setenv("APP_FRONTEND_URL", "https://app.example")

	got, err := svc.SendAccountInvite(inviteCtx, inviteID)
	if err == nil || err.Error() != "invite mail: send failed" || got != inviteLink {
		t.Fatalf("invite got %q err %v", got, err)
	}
	if refreshes != 1 || mints != 0 {
		t.Fatalf("refreshes = %d mints = %d", refreshes, mints)
	}
	assertUnknownSendWarn(t, logs.String(), PurposeAccountInvite, "invite-secret", inviteLink)
}

func TestKnown_Send_FailureDoesNotLogUnknownOutcome(t *testing.T) {
	userID := uuid.New()
	logs := captureCredentialMailLogs(t)
	svc := credentialService(t, userID, func() (string, error) { return "raw-token", nil }, senderFunc{
		resetErr: errors.New("connection refused"),
	})
	if err := svc.SendPasswordReset(context.Background(), userID); err == nil {
		t.Fatal("expected a send failure")
	}
	if logs.Len() != 0 {
		t.Fatalf("known failure logged %q", logs.String())
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := svc.SendPasswordReset(ctx, userID); err == nil {
		t.Fatal("expected a send failure")
	}
	if logs.Len() != 0 {
		t.Fatalf("deadline before send logged %q", logs.String())
	}
}

func TestCompleted_Send_DoesNotLogUnknownOutcome(t *testing.T) {
	userID := uuid.New()
	logs := captureCredentialMailLogs(t)
	ctx := newMidSendDeadline()
	svc := credentialService(t, userID, func() (string, error) { return "raw-token", nil }, senderFunc{
		reset: func(string, string) { ctx.hit() },
	})
	if err := svc.SendPasswordReset(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("completed send logged %q", logs.String())
	}
}

// midSendDeadline reports DeadlineExceeded only after hit, which the sender
// calls while Mail().Send is in progress.
type midSendDeadline struct {
	context.Context
	fired chan struct{}
}

func newMidSendDeadline() *midSendDeadline {
	return &midSendDeadline{Context: context.Background(), fired: make(chan struct{})}
}

func (m *midSendDeadline) hit() {
	select {
	case <-m.fired:
	default:
		close(m.fired)
	}
}

func (m *midSendDeadline) Err() error {
	select {
	case <-m.fired:
		return context.DeadlineExceeded
	default:
		return m.Context.Err()
	}
}

func credentialService(t *testing.T, userID uuid.UUID, mint func() (string, error), sender senderFunc) *Service {
	t.Helper()
	return NewService(Deps{
		Users: userLookupFunc(func(_ context.Context, id uuid.UUID) (*models.User, error) {
			if id != userID {
				t.Fatalf("lookup id = %s", id)
			}
			return &models.User{ID: userID, Email: "person@example.com"}, nil
		}),
		Tokens:         tokenIssuerFunc{mint: mint, hash: func(string) string { return "stored-hash" }},
		Resets:         resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites:        inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:         sender,
		Dispatch:       func(uuid.UUID, string) error { t.Fatal("send must not enqueue"); return nil },
		DispatchInvite: func(uuid.UUID) (string, error) { t.Fatal("send must not dispatch"); return "", nil },
	})
}

func captureCredentialMailLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func assertUnknownSendWarn(t *testing.T, logs, purpose string, forbidden ...string) {
	t.Helper()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "credential mail send outcome unknown") || !strings.Contains(logs, "purpose="+purpose) {
		t.Fatalf("log = %q", logs)
	}
	if strings.Contains(logs, "level=ERROR") {
		t.Fatalf("log used ERROR: %q", logs)
	}
	for _, secret := range forbidden {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("log leaked %q in %q", secret, logs)
		}
	}
}

func TestDispatch_Payload_IsSubjectAndPurpose(t *testing.T) {
	subjectID := uuid.New()
	var gotID uuid.UUID
	var gotPurpose string
	svc := NewService(Deps{
		Users:    userLookupFunc(func(context.Context, uuid.UUID) (*models.User, error) { return nil, nil }),
		Tokens:   tokenIssuerFunc{mint: func() (string, error) { return "raw", nil }, hash: func(string) string { return "hash" }},
		Resets:   resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites:  inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:   senderFunc{},
		Dispatch: func(id uuid.UUID, purpose string) error { gotID, gotPurpose = id, purpose; return nil },
		DispatchInvite: func(uuid.UUID) (string, error) {
			t.Fatal("purpose dispatch must not dispatch an invite")
			return "", nil
		},
	})

	if err := svc.Dispatch(subjectID, PurposePasswordReset); err != nil {
		t.Fatal(err)
	}
	if gotID != subjectID || gotPurpose != PurposePasswordReset {
		t.Fatalf("dispatched %s %q", gotID, gotPurpose)
	}
	if strings.Contains(gotPurpose, "token") || strings.Contains(gotID.String(), "token") {
		t.Fatal("payload carried a credential")
	}
	if err := svc.Dispatch(subjectID, "token=secret"); err == nil {
		t.Fatal("unknown purpose must be refused")
	}
	if err := svc.Dispatch(uuid.Nil, PurposeAccountInvite); err == nil {
		t.Fatal("nil subject must be refused")
	}
	if err := svc.Dispatch(subjectID, PurposeWelcome); err == nil || gotPurpose != PurposePasswordReset {
		t.Fatal("welcome must not be queued")
	}
}

func TestDispatch_Account_InvitePassesOnlyTheInviteID(t *testing.T) {
	inviteID := uuid.New()
	const link = "https://app.example/accept-invite?token=kept-off-queue"
	var gotID uuid.UUID
	svc := NewService(Deps{
		Users:    userLookupFunc(func(context.Context, uuid.UUID) (*models.User, error) { return nil, nil }),
		Tokens:   tokenIssuerFunc{mint: func() (string, error) { return "", nil }, hash: func(string) string { return "" }},
		Resets:   resetWriterFunc(func(context.Context, *models.PasswordResetToken) error { return nil }),
		Invites:  inviteRefresherFunc(func(context.Context, uuid.UUID, string) (account.InviteMail, error) { return account.InviteMail{}, nil }),
		Sender:   senderFunc{},
		Dispatch: func(uuid.UUID, string) error { t.Fatal("add-user invite must use the invite dispatcher"); return nil },
		DispatchInvite: func(id uuid.UUID) (string, error) {
			gotID = id
			return link, nil
		},
	})

	got, err := svc.DispatchAccountInvite(inviteID)
	if err != nil {
		t.Fatal(err)
	}
	if got != link || gotID != inviteID {
		t.Fatal("dispatch did not return the minted link for the invite id")
	}
	if _, err := svc.DispatchAccountInvite(uuid.Nil); err == nil {
		t.Fatal("nil invite must be refused")
	}
}

type userLookupFunc func(context.Context, uuid.UUID) (*models.User, error)

func (f userLookupFunc) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	return f(ctx, id)
}

type tokenIssuerFunc struct {
	mint func() (string, error)
	hash func(string) string
}

func (f tokenIssuerFunc) GenerateRandomToken() (string, error) { return f.mint() }
func (f tokenIssuerFunc) HashToken(raw string) string          { return f.hash(raw) }

type resetWriterFunc func(context.Context, *models.PasswordResetToken) error

func (f resetWriterFunc) Create(ctx context.Context, token *models.PasswordResetToken) error {
	return f(ctx, token)
}

type inviteRefresherFunc func(context.Context, uuid.UUID, string) (account.InviteMail, error)

func (f inviteRefresherFunc) RefreshInviteForMail(ctx context.Context, inviteID uuid.UUID, frontendBase string) (account.InviteMail, error) {
	return f(ctx, inviteID, frontendBase)
}

type senderFunc struct {
	invite    func(account.InviteMail)
	reset     func(to, link string)
	welcome   func(to, fullName string)
	inviteErr error
	resetErr  error
}

func (f senderFunc) SendInvite(ctx context.Context, message account.InviteMail) error {
	if f.invite != nil {
		f.invite(message)
	}
	return f.inviteErr
}

func (f senderFunc) SendReset(ctx context.Context, to, resetLink string) error {
	if f.reset != nil {
		f.reset(to, resetLink)
	}
	return f.resetErr
}

func (f senderFunc) SendWelcome(_ context.Context, to, fullName string) error {
	if f.welcome != nil {
		f.welcome(to, fullName)
	}
	return nil
}
