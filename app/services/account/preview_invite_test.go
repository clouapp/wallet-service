package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type previewInvites struct {
	InviteStore
	invite *models.AccountInvite
}

func (s previewInvites) FindPendingByTokenHash(context.Context, string, time.Time) (*models.AccountInvite, error) {
	if s.invite == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return s.invite, nil
}

type previewUsers struct {
	UserStore
	invitee *models.User
	inviter *models.User
	err     error
}

func (u previewUsers) FindByEmail(context.Context, string) (*models.User, error) {
	if u.invitee == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return u.invitee, nil
}

func (u previewUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	if u.err != nil {
		return nil, u.err
	}
	return u.inviter, nil
}

type previewAccounts struct {
	AccountStore
	account *models.Account
	err     error
}

func (a previewAccounts) FindByID(context.Context, uuid.UUID) (*models.Account, error) {
	return a.account, a.err
}

func pendingInvite() *models.AccountInvite {
	inviter := uuid.New()
	return &models.AccountInvite{
		ID: uuid.New(), AccountID: uuid.New(), Email: "new@example.com", Role: models.AccountRoleAuditor,
		InvitedBy: &inviter, ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestPreview_Invite_NamesTheAccountAndTheInviter(t *testing.T) {
	svc := NewService(Deps{
		Invites:  previewInvites{invite: pendingInvite()},
		Users:    previewUsers{inviter: &models.User{FullName: "Ada Lovelace", Email: "ada@example.com"}},
		Accounts: previewAccounts{account: &models.Account{Name: "Acme"}},
	})

	preview, err := svc.PreviewInvite(context.Background(), "raw-token")
	if err != nil {
		t.Fatal(err)
	}
	want := InvitePreview{AccountName: "Acme", Inviter: "Ada Lovelace", Role: models.AccountRoleAuditor, Email: "new@example.com", NeedsPassword: true}
	if preview != want {
		t.Fatalf("preview = %+v, want %+v", preview, want)
	}
}

func TestPreview_Invite_NamesAnInviterWithoutANameByEmail(t *testing.T) {
	svc := NewService(Deps{
		Invites:  previewInvites{invite: pendingInvite()},
		Users:    previewUsers{inviter: &models.User{Email: "ada@example.com"}, invitee: &models.User{Email: "new@example.com"}},
		Accounts: previewAccounts{account: &models.Account{Name: "Acme"}},
	})

	preview, err := svc.PreviewInvite(context.Background(), "raw-token")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Inviter != "ada@example.com" || preview.NeedsPassword {
		t.Fatalf("preview = %+v, want the inviter's email and no password for an existing user", preview)
	}
}

func TestPreview_Invite_LeavesANameItCannotReadBlank(t *testing.T) {
	outage := errors.New("store unavailable")
	svc := NewService(Deps{
		Invites:  previewInvites{invite: pendingInvite()},
		Users:    previewUsers{err: outage},
		Accounts: previewAccounts{err: outage},
	})

	preview, err := svc.PreviewInvite(context.Background(), "raw-token")
	if err != nil {
		t.Fatal(err)
	}
	if preview.AccountName != "" || preview.Inviter != "" || preview.Email != "new@example.com" {
		t.Fatalf("preview = %+v", preview)
	}
}

func TestPreview_Invite_RefusesATokenWithNoPendingInvite(t *testing.T) {
	svc := NewService(Deps{Invites: previewInvites{}, Users: previewUsers{}, Accounts: previewAccounts{}})

	if _, err := svc.PreviewInvite(context.Background(), "raw-token"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("err = %v, want ErrInviteInvalid", err)
	}
}
