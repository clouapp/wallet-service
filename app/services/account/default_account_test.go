package account

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type defaultMemberships struct {
	MembershipStore
	member *models.AccountUser
	err    error
}

func (m defaultMemberships) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return m.member, m.err
}

type defaultUsers struct {
	UserStore
	user     *models.User
	written  []uuid.UUID
	writeErr error
}

func (u *defaultUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	if u.user == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return u.user, nil
}

func (u *defaultUsers) UpdateDefaultAccountID(_ context.Context, _ uuid.UUID, accountID *uuid.UUID) error {
	if u.writeErr != nil {
		return u.writeErr
	}
	u.written = append(u.written, *accountID)
	return nil
}

type defaultAccounts struct {
	AccountStore
	account *models.Account
}

func (a defaultAccounts) FindByID(context.Context, uuid.UUID) (*models.Account, error) {
	if a.account == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return a.account, nil
}

func TestSet_Default_AccountWritesItAndReturnsItsView(t *testing.T) {
	account := &models.Account{ID: uuid.New(), Name: "Acme"}
	limits := `{"daily":"5"}`
	users := &defaultUsers{user: &models.User{ID: uuid.New()}}
	svc := NewService(Deps{
		Memberships: defaultMemberships{member: &models.AccountUser{AccountID: account.ID}},
		Users:       users,
		Accounts:    defaultAccounts{account: account},
		SweepLimits: fixedSweepLimits{document: &limits},
	})

	view, err := svc.SetDefaultAccount(context.Background(), users.user.ID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(users.written) != 1 || users.written[0] != account.ID {
		t.Fatalf("default account writes = %v", users.written)
	}
	if view == nil || view.Account.ID != account.ID || view.SweepLimits == nil || *view.SweepLimits != limits {
		t.Fatalf("view = %+v", view)
	}
}

func TestSet_Default_AccountRefusesAnAccountTheUserIsNotIn(t *testing.T) {
	for name, memberships := range map[string]defaultMemberships{
		"no membership":    {err: models.ErrRepositoryNotFound},
		"a failed read":    {err: errors.New("store unavailable")},
		"a nil membership": {},
	} {
		t.Run(name, func(t *testing.T) {
			users := &defaultUsers{user: &models.User{ID: uuid.New()}}
			svc := NewService(Deps{Memberships: memberships, Users: users, Accounts: defaultAccounts{}})

			_, err := svc.SetDefaultAccount(context.Background(), users.user.ID, uuid.New())
			if !errors.Is(err, ErrNotMember) {
				t.Fatalf("err = %v, want ErrNotMember", err)
			}
			if len(users.written) != 0 {
				t.Fatal("a default account was written for a non-member")
			}
		})
	}
}

func TestSet_Default_AccountNeedsTheUser(t *testing.T) {
	users := &defaultUsers{}
	svc := NewService(Deps{
		Memberships: defaultMemberships{member: &models.AccountUser{}},
		Users:       users,
		Accounts:    defaultAccounts{},
	})

	if _, err := svc.SetDefaultAccount(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestSet_Default_AccountReturnsTheFailedWrite(t *testing.T) {
	outage := errors.New("store unavailable")
	users := &defaultUsers{user: &models.User{ID: uuid.New()}, writeErr: outage}
	svc := NewService(Deps{
		Memberships: defaultMemberships{member: &models.AccountUser{}},
		Users:       users,
		Accounts:    defaultAccounts{},
	})

	if _, err := svc.SetDefaultAccount(context.Background(), users.user.ID, uuid.New()); !errors.Is(err, outage) {
		t.Fatalf("err = %v, want the write failure", err)
	}
}

func TestSet_Default_AccountServesNoViewForAnAccountThatIsGone(t *testing.T) {
	users := &defaultUsers{user: &models.User{ID: uuid.New()}}
	svc := NewService(Deps{
		Memberships: defaultMemberships{member: &models.AccountUser{}},
		Users:       users,
		Accounts:    defaultAccounts{},
	})

	view, err := svc.SetDefaultAccount(context.Background(), users.user.ID, uuid.New())
	if err != nil || view != nil {
		t.Fatalf("view = %+v, err = %v, want no view and no error", view, err)
	}
	if len(users.written) != 1 {
		t.Fatal("the default account was not written")
	}
}
