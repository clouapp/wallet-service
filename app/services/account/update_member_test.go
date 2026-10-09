package account

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// changeMemberships holds an owner and one member of one account and records
// the column writes a change makes.
type changeMemberships struct {
	MembershipStore
	owner, member models.AccountUser
	writes        []string
}

func (m *changeMemberships) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (m *changeMemberships) row(userID uuid.UUID) (*models.AccountUser, error) {
	switch userID {
	case m.owner.UserID:
		owner := m.owner
		return &owner, nil
	case m.member.UserID:
		member := m.member
		return &member, nil
	}
	return nil, models.ErrRepositoryNotFound
}

func (m *changeMemberships) FindByAccountAndUser(_ context.Context, _, userID uuid.UUID) (*models.AccountUser, error) {
	return m.row(userID)
}

func (m *changeMemberships) FindByAccountAndUserIncludeDeleted(_ context.Context, _, userID uuid.UUID) (*models.AccountUser, error) {
	return m.row(userID)
}

func (m *changeMemberships) SetRole(_ context.Context, _ uuid.UUID, role string) error {
	m.writes = append(m.writes, "role="+role)
	m.member.Role = role
	return nil
}

func (m *changeMemberships) SetStatus(_ context.Context, _ uuid.UUID, status string) error {
	m.writes = append(m.writes, "status="+status)
	m.member.Status = status
	return nil
}

func newChangeFixture() (*Service, *changeMemberships) {
	accountID := uuid.New()
	store := &changeMemberships{
		owner: models.AccountUser{ID: uuid.New(), AccountID: accountID, UserID: uuid.New(),
			Role: models.AccountRoleOwner, Status: models.MembershipStatusActive},
		member: models.AccountUser{ID: uuid.New(), AccountID: accountID, UserID: uuid.New(),
			Role: models.AccountRoleUser, Status: models.MembershipStatusActive},
	}
	return NewService(Deps{Memberships: store, Activity: &addActivity{}}), store
}

func (m *changeMemberships) input(role, status string) UpdateMemberInput {
	return UpdateMemberInput{
		AccountID: m.owner.AccountID, ActorID: m.owner.UserID, TargetID: m.member.UserID,
		Role: role, Status: status,
	}
}

func TestUpdate_Member_LeavesABlankFieldAsStored(t *testing.T) {
	svc, store := newChangeFixture()

	updated, err := svc.UpdateMember(context.Background(), store.input("", models.MembershipStatusSuspended))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 || store.writes[0] != "status=suspended" {
		t.Fatalf("writes = %v, want only the status", store.writes)
	}
	if updated.Role != models.AccountRoleUser || updated.Status != models.MembershipStatusSuspended {
		t.Fatalf("updated = %+v", updated)
	}

	svc, store = newChangeFixture()
	if _, err := svc.UpdateMember(context.Background(), store.input(models.AccountRoleAdmin, "")); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 || store.writes[0] != "role=admin" {
		t.Fatalf("writes = %v, want only the role", store.writes)
	}
}

func TestUpdate_Member_RefusesAChangeOfNothingAndUnknownValues(t *testing.T) {
	cases := map[string]struct {
		role, status string
		want         error
	}{
		"neither field":  {want: ErrMemberChangeEmpty},
		"unknown role":   {role: "viewer", want: ErrMemberRole},
		"unknown status": {status: "deleted", want: ErrMemberStatus},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store := newChangeFixture()
			_, err := svc.UpdateMember(context.Background(), store.input(tc.role, tc.status))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(store.writes) != 0 {
				t.Fatalf("a refused change wrote %v", store.writes)
			}
		})
	}
}
