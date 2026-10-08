package account

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type pagedAccounts struct {
	AccountStore
	rows  []models.Account
	total int64
	err   error
}

func (a pagedAccounts) PaginateByMember(context.Context, uuid.UUID, string, string, int, int) ([]models.Account, int64, error) {
	return a.rows, a.total, a.err
}

type roleMemberships struct {
	MembershipStore
	roles map[uuid.UUID]string
	err   error
	reads int
}

func (m *roleMemberships) RolesForUserAccounts(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]string, error) {
	m.reads++
	return m.roles, m.err
}

func TestList_For_MemberWithRolesPairsEachAccountWithTheCallersRole(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	svc := NewService(Deps{
		Accounts:    pagedAccounts{rows: []models.Account{{ID: a}, {ID: b}}, total: 7},
		Memberships: &roleMemberships{roles: map[uuid.UUID]string{a: models.AccountRoleOwner, b: models.AccountRoleAuditor}},
	})

	got, total, err := svc.ListForMemberWithRoles(context.Background(), uuid.New(), "", "", 20, 0)
	if err != nil || total != 7 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	if len(got) != 2 || got[0].Account.ID != a || got[0].Role != models.AccountRoleOwner || got[1].Account.ID != b || got[1].Role != models.AccountRoleAuditor {
		t.Fatalf("items = %+v", got)
	}
}

func TestList_For_MemberWithRolesReadsNoRolesForAnEmptyPage(t *testing.T) {
	memberships := &roleMemberships{}
	svc := NewService(Deps{Accounts: pagedAccounts{total: 0}, Memberships: memberships})

	got, total, err := svc.ListForMemberWithRoles(context.Background(), uuid.New(), "", "", 20, 0)
	if err != nil || total != 0 || got == nil || len(got) != 0 {
		t.Fatalf("got = %#v, total = %d, err = %v; want an empty non-nil page", got, total, err)
	}
	if memberships.reads != 0 {
		t.Fatalf("role reads = %d, want 0", memberships.reads)
	}
}

func TestList_For_MemberWithRolesFailsWhenAnAccountHasNoRole(t *testing.T) {
	a := uuid.New()
	for name, roles := range map[string]map[uuid.UUID]string{
		"missing":    {},
		"blank role": {a: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewService(Deps{
				Accounts:    pagedAccounts{rows: []models.Account{{ID: a}}, total: 1},
				Memberships: &roleMemberships{roles: roles},
			})
			if _, _, err := svc.ListForMemberWithRoles(context.Background(), uuid.New(), "", "", 20, 0); err == nil {
				t.Fatal("a listed account without a role was served")
			}
		})
	}
}

func TestList_For_MemberWithRolesReturnsTheStoreErrors(t *testing.T) {
	boom := errors.New("store down")
	svc := NewService(Deps{Accounts: pagedAccounts{err: boom}, Memberships: &roleMemberships{}})
	if _, _, err := svc.ListForMemberWithRoles(context.Background(), uuid.New(), "", "", 20, 0); !errors.Is(err, boom) {
		t.Fatalf("account read err = %v", err)
	}

	a := uuid.New()
	svc = NewService(Deps{
		Accounts:    pagedAccounts{rows: []models.Account{{ID: a}}, total: 1},
		Memberships: &roleMemberships{err: boom},
	})
	if _, _, err := svc.ListForMemberWithRoles(context.Background(), uuid.New(), "", "", 20, 0); !errors.Is(err, boom) {
		t.Fatalf("role read err = %v", err)
	}
}
