package credentialmail

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

func TestSendPasswordResetStoresOnlyTheHash(t *testing.T) {
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
			hash: authsvc.NewService().HashToken,
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
	if !authsvc.NewService().CheckToken(raw, stored) {
		t.Fatal("stored value is not the hash of the minted token")
	}
	if sentTo != "person@example.com" || !strings.Contains(sentLink, raw) || strings.Contains(sentLink, stored) {
		t.Fatalf("sent to %q link %q", sentTo, sentLink)
	}
}

func TestSendAccountInviteDoesNotEnqueueTheLink(t *testing.T) {
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

func TestSendRunsTheDecodedPurpose(t *testing.T) {
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

	link, err := svc.Send(context.Background(), userID, PurposeWelcome)
	if err != nil || link != "" || welcome != 1 {
		t.Fatalf("link %q err %v welcome %d", link, err, welcome)
	}
	if _, err := svc.Send(context.Background(), userID, "not-a-purpose"); err == nil {
		t.Fatal("unknown purpose must be refused")
	}
}

func TestSendWelcomeUsesTheLoadedUser(t *testing.T) {
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

func TestDispatchPayloadIsSubjectAndPurpose(t *testing.T) {
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
}

func TestDispatchAccountInvitePassesOnlyTheInviteID(t *testing.T) {
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
	invite  func(account.InviteMail)
	reset   func(to, link string)
	welcome func(to, fullName string)
}

func (f senderFunc) SendInvite(ctx context.Context, message account.InviteMail) error {
	if f.invite != nil {
		f.invite(message)
	}
	return nil
}

func (f senderFunc) SendReset(ctx context.Context, to, resetLink string) error {
	if f.reset != nil {
		f.reset(to, resetLink)
	}
	return nil
}

func (f senderFunc) SendWelcome(_ context.Context, to, fullName string) error {
	if f.welcome != nil {
		f.welcome(to, fullName)
	}
	return nil
}
