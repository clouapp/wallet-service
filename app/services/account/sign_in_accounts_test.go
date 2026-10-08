package account

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type signInMemberships struct {
	MembershipStore
	rows []models.AccountUser
	err  error
}

func (m signInMemberships) FindByUserID(context.Context, uuid.UUID) ([]models.AccountUser, error) {
	return m.rows, m.err
}

type signInAccounts struct {
	AccountStore
	byID map[uuid.UUID]*models.Account
	err  map[uuid.UUID]error
}

func (a signInAccounts) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	return a.byID[id], a.err[id]
}

func signInAccount(id string, environment string) *models.Account {
	return &models.Account{ID: uuid.MustParse(id), Environment: environment}
}

const (
	idA = "00000000-0000-0000-0000-00000000000a"
	idB = "00000000-0000-0000-0000-00000000000b"
	idC = "00000000-0000-0000-0000-00000000000c"
)

func TestSign_In_AccountsSortByEnvironmentThenID(t *testing.T) {
	accounts := signInAccounts{byID: map[uuid.UUID]*models.Account{
		uuid.MustParse(idA): signInAccount(idA, models.EnvironmentTest),
		uuid.MustParse(idB): signInAccount(idB, models.EnvironmentProd),
		uuid.MustParse(idC): signInAccount(idC, models.EnvironmentProd),
	}}
	memberships := signInMemberships{rows: []models.AccountUser{
		{AccountID: uuid.MustParse(idC), Role: models.AccountRoleUser},
		{AccountID: uuid.MustParse(idA), Role: models.AccountRoleOwner},
		{AccountID: uuid.MustParse(idB), Role: models.AccountRoleAdmin},
	}}
	svc := NewService(Deps{Accounts: accounts, Memberships: memberships})

	got, err := svc.SignInAccounts(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{idB, idC, idA}
	wantRoles := []string{models.AccountRoleAdmin, models.AccountRoleUser, models.AccountRoleOwner}
	if len(got.Accounts) != 3 {
		t.Fatalf("accounts = %d, want 3", len(got.Accounts))
	}
	for i, entry := range got.Accounts {
		if entry.Account.ID.String() != wantOrder[i] || entry.Role != wantRoles[i] {
			t.Fatalf("entry %d = %s/%s, want %s/%s", i, entry.Account.ID, entry.Role, wantOrder[i], wantRoles[i])
		}
	}
}

func TestSign_In_AccountsDefaultToTheUsersDefaultThenTheFirst(t *testing.T) {
	accounts := signInAccounts{byID: map[uuid.UUID]*models.Account{
		uuid.MustParse(idA): signInAccount(idA, models.EnvironmentProd),
		uuid.MustParse(idB): signInAccount(idB, models.EnvironmentTest),
	}}
	memberships := signInMemberships{rows: []models.AccountUser{
		{AccountID: uuid.MustParse(idA), Role: models.AccountRoleOwner},
		{AccountID: uuid.MustParse(idB), Role: models.AccountRoleOwner},
	}}
	svc := NewService(Deps{Accounts: accounts, Memberships: memberships})

	preferred := uuid.MustParse(idB)
	got, err := svc.SignInAccounts(context.Background(), uuid.New(), &preferred)
	if err != nil || got.DefaultID != preferred {
		t.Fatalf("default = %v, %v; want %v", got.DefaultID, err, preferred)
	}

	got, err = svc.SignInAccounts(context.Background(), uuid.New(), nil)
	if err != nil || got.DefaultID.String() != idA {
		t.Fatalf("fallback default = %v, %v; want the first sorted account %s", got.DefaultID, err, idA)
	}

	stale := uuid.New()
	got, err = svc.SignInAccounts(context.Background(), uuid.New(), &stale)
	if err != nil || got.DefaultID.String() != idA {
		t.Fatalf("a default outside the memberships = %v, %v; want the first sorted account %s", got.DefaultID, err, idA)
	}
}

func TestSign_In_AccountsSkipUnreadableAccountsAndHaveNoDefaultWhenEmpty(t *testing.T) {
	gone := uuid.MustParse(idA)
	accounts := signInAccounts{
		byID: map[uuid.UUID]*models.Account{uuid.MustParse(idB): nil},
		err:  map[uuid.UUID]error{gone: errors.New("store down")},
	}
	memberships := signInMemberships{rows: []models.AccountUser{
		{AccountID: gone, Role: models.AccountRoleOwner},
		{AccountID: uuid.MustParse(idB), Role: models.AccountRoleOwner},
	}}
	svc := NewService(Deps{Accounts: accounts, Memberships: memberships})

	got, err := svc.SignInAccounts(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 0 || got.DefaultID != uuid.Nil {
		t.Fatalf("result = %+v, want no accounts and no default", got)
	}
}

func TestSign_In_AccountsReturnTheMembershipReadError(t *testing.T) {
	boom := errors.New("membership store unavailable")
	svc := NewService(Deps{Accounts: signInAccounts{}, Memberships: signInMemberships{err: boom}})

	if _, err := svc.SignInAccounts(context.Background(), uuid.New(), nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the membership store error", err)
	}
}
