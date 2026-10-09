package account_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// openInviteStore answers FindOpenByAccountAndID only; the embedded interface
// is nil, so any other call would panic.
type openInviteStore struct {
	accountsvc.InviteStore
	row       *models.AccountInvite
	err       error
	accountID uuid.UUID
}

func (s *openInviteStore) FindOpenByAccountAndID(_ context.Context, accountID, _ uuid.UUID) (*models.AccountInvite, error) {
	s.accountID = accountID
	return s.row, s.err
}

func TestFind_Open_InviteAsksTheStoreForTheAccountsInvite(t *testing.T) {
	accountID := uuid.New()
	want := &models.AccountInvite{ID: uuid.New()}
	store := &openInviteStore{row: want}
	svc := accountsvc.NewService(accountsvc.Deps{Invites: store})

	got, err := svc.FindOpenInvite(context.Background(), accountID, want.ID)
	if err != nil || got != want || store.accountID != accountID {
		t.Fatalf("got %v, %v, account %v", got, err, store.accountID)
	}

	store.err = models.ErrRepositoryNotFound
	if _, err := svc.FindOpenInvite(context.Background(), accountID, want.ID); !errors.Is(err, models.ErrRepositoryNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	if _, err := svc.FindOpenInvite(nil, accountID, want.ID); err == nil {
		t.Fatal("a nil context must be refused")
	}
	if _, err := accountsvc.NewService(accountsvc.Deps{}).FindOpenInvite(context.Background(), accountID, want.ID); err == nil {
		t.Fatal("a service without an invite store must refuse")
	}
}

func TestInvite_Link_IsARealTokenAndIsNotQueued(t *testing.T) {
	link, err := accountsvc.InviteLink("https://app.example", "tok_abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(link, "token=tok_abc") || strings.Contains(link, "vault.app/accept-invite") {
		t.Fatalf("link = %s", link)
	}
	if _, err := accountsvc.InviteLink("https://app.example", ""); err == nil {
		t.Fatal("empty token must be refused")
	}
	mail := &mails.UserInviteMail{InviteLink: link}
	if mail.Queue() != nil {
		t.Fatal("invite mail must not put the token on a queue")
	}
	hash := accountsvc.HashInviteToken("tok_abc")
	if hash == "" || hash == "tok_abc" || !accountsvc.InviteTokenMatches(hash, "tok_abc") || accountsvc.InviteTokenMatches(hash, "other") {
		t.Fatal("only the hash of the token may be stored")
	}
}
