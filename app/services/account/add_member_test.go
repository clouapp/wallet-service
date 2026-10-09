package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// addMemberships is the membership store of one account: the caller's
// membership and the rows the add writes. readErr fails every read after the
// first, the one that authorizes the caller.
type addMemberships struct {
	MembershipStore
	actor   *models.AccountUser
	created []models.AccountUser
	reads   int
	readErr error
	addErr  error
}

func (m *addMemberships) FindByAccountAndUser(_ context.Context, _, userID uuid.UUID) (*models.AccountUser, error) {
	m.reads++
	if m.reads > 1 && m.readErr != nil {
		return nil, m.readErr
	}
	if m.actor != nil && userID == m.actor.UserID {
		return m.actor, nil
	}
	for i := range m.created {
		if m.created[i].UserID == userID {
			return &m.created[i], nil
		}
	}
	return nil, models.ErrRepositoryNotFound
}

func (m *addMemberships) FindByAccountAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, models.ErrRepositoryNotFound
}

func (m *addMemberships) Create(_ context.Context, member *models.AccountUser) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.created = append(m.created, *member)
	return nil
}

type addUsers struct {
	UserStore
	user *models.User
	err  error
}

func (u addUsers) FindByEmail(context.Context, string) (*models.User, error) {
	if u.err != nil {
		return nil, u.err
	}
	if u.user == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return u.user, nil
}

type addInvites struct {
	InviteStore
	created []models.AccountInvite
}

func (s *addInvites) FindPendingByAccountEmail(context.Context, uuid.UUID, string) (*models.AccountInvite, error) {
	return nil, models.ErrRepositoryNotFound
}

func (s *addInvites) Create(_ context.Context, invite *models.AccountInvite) error {
	s.created = append(s.created, *invite)
	return nil
}

type addActivity struct{ rows []models.AccountActivity }

func (a *addActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (a *addActivity) Append(_ context.Context, row models.AccountActivity) error {
	a.rows = append(a.rows, row)
	return nil
}

type addFixture struct {
	accountID   uuid.UUID
	ownerID     uuid.UUID
	memberships *addMemberships
	invites     *addInvites
	activity    *addActivity
}

func newAddFixture(users addUsers) (*Service, *addFixture) {
	f := &addFixture{accountID: uuid.New(), ownerID: uuid.New(), invites: &addInvites{}, activity: &addActivity{}}
	f.memberships = &addMemberships{actor: &models.AccountUser{
		ID: uuid.New(), AccountID: f.accountID, UserID: f.ownerID,
		Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}}
	svc := NewService(Deps{
		Memberships: f.memberships,
		Users:       users,
		Invites:     f.invites,
		Activity:    f.activity,
	})
	return svc, f
}

func (f *addFixture) input(email, role string) AddMemberInput {
	return AddMemberInput{AccountID: f.accountID, ActorID: f.ownerID, Email: email, Role: role}
}

func TestAdd_Member_AddsTheUserThatOwnsTheEmail(t *testing.T) {
	target := &models.User{ID: uuid.New(), Email: "member@example.com"}
	svc, f := newAddFixture(addUsers{user: target})

	added, err := svc.AddMember(context.Background(), f.input(target.Email, models.AccountRoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	if added.Invite != nil {
		t.Fatal("an existing user was invited")
	}
	if added.Member == nil || added.Member.UserID != target.ID || added.Member.Role != models.AccountRoleAdmin {
		t.Fatalf("member = %+v", added.Member)
	}
	if len(f.invites.created) != 0 {
		t.Fatal("an invite row was written for an existing user")
	}
}

func TestAdd_Member_InvitesAnEmailNoUserOwns(t *testing.T) {
	t.Setenv(frontendURLEnv, "https://wallet.example")
	svc, f := newAddFixture(addUsers{})

	added, err := svc.AddMember(context.Background(), f.input("new@example.com", models.AccountRoleUser))
	if err != nil {
		t.Fatal(err)
	}
	if added.Member != nil {
		t.Fatal("an unknown email became a member")
	}
	if added.Invite == nil || added.Invite.Invite == nil || added.Invite.Invite.Email != "new@example.com" {
		t.Fatalf("invite = %+v", added.Invite)
	}
	if len(f.invites.created) != 1 || len(f.memberships.created) != 0 {
		t.Fatalf("invites %d, memberships %d", len(f.invites.created), len(f.memberships.created))
	}
}

func TestAdd_Member_NeedsTheFrontendURLToInvite(t *testing.T) {
	t.Setenv(frontendURLEnv, "")
	svc, f := newAddFixture(addUsers{})

	_, err := svc.AddMember(context.Background(), f.input("new@example.com", models.AccountRoleUser))
	if !errors.Is(err, ErrFrontendURLRequired) {
		t.Fatalf("err = %v, want ErrFrontendURLRequired", err)
	}
	if len(f.invites.created) != 0 {
		t.Fatal("an invite was stored without a link base")
	}
}

func TestAdd_Member_RefusesARoleAboveTheCallerOnBothPaths(t *testing.T) {
	t.Setenv(frontendURLEnv, "https://wallet.example")
	for name, users := range map[string]addUsers{
		"existing user": {user: &models.User{ID: uuid.New(), Email: "member@example.com"}},
		"invite":        {},
	} {
		t.Run(name, func(t *testing.T) {
			svc, f := newAddFixture(users)
			f.memberships.actor.Role = models.AccountRoleAdmin

			_, err := svc.AddMember(context.Background(), f.input("member@example.com", models.AccountRoleOwner))
			if !errors.Is(err, ErrGrantRole) {
				t.Fatalf("err = %v, want ErrGrantRole", err)
			}
			if len(f.memberships.created) != 0 || len(f.invites.created) != 0 {
				t.Fatal("a refused grant wrote a row")
			}
		})
	}
}

func TestAdd_Member_NamesTheStepThatFailed(t *testing.T) {
	outage := errors.New("store unavailable")
	target := &models.User{ID: uuid.New(), Email: "member@example.com"}

	t.Run("user lookup", func(t *testing.T) {
		svc, f := newAddFixture(addUsers{err: outage})
		_, err := svc.AddMember(context.Background(), f.input(target.Email, models.AccountRoleUser))
		if !errors.Is(err, ErrUserLookup) || !errors.Is(err, outage) {
			t.Fatalf("err = %v, want ErrUserLookup wrapping the cause", err)
		}
	})
	t.Run("membership write", func(t *testing.T) {
		svc, f := newAddFixture(addUsers{user: target})
		f.memberships.addErr = outage
		_, err := svc.AddMember(context.Background(), f.input(target.Email, models.AccountRoleUser))
		if !errors.Is(err, ErrMemberNotAdded) || !errors.Is(err, outage) {
			t.Fatalf("err = %v, want ErrMemberNotAdded wrapping the cause", err)
		}
	})
	t.Run("membership read back", func(t *testing.T) {
		svc, f := newAddFixture(addUsers{user: target})
		f.memberships.readErr = outage
		_, err := svc.AddMember(context.Background(), f.input(target.Email, models.AccountRoleUser))
		if !errors.Is(err, ErrMemberUnreadable) || !errors.Is(err, outage) {
			t.Fatalf("err = %v, want ErrMemberUnreadable wrapping the cause", err)
		}
		if len(f.memberships.created) != 1 {
			t.Fatal("the membership write did not run")
		}
	})
}

func TestAdd_Member_LeavesAMembershipThatCannotBeFoundBackAsNoMember(t *testing.T) {
	target := &models.User{ID: uuid.New(), Email: "member@example.com"}
	svc, f := newAddFixture(addUsers{user: target})
	f.memberships.readErr = models.ErrRepositoryNotFound

	added, err := svc.AddMember(context.Background(), f.input(target.Email, models.AccountRoleUser))
	if err != nil {
		t.Fatal(err)
	}
	if added.Member != nil || added.Invite != nil {
		t.Fatalf("added = %+v, want neither a member nor an invite", added)
	}
}

func TestInvite_Member_IssuesTheInviteWithTheFrontendLink(t *testing.T) {
	t.Setenv(frontendURLEnv, "https://wallet.example")
	svc, f := newAddFixture(addUsers{})

	issued, err := svc.InviteMember(context.Background(), InviteInput{
		AccountID: f.accountID, InvitedBy: f.ownerID, Email: "Invitee@Example.com ", Role: models.AccountRoleAuditor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Invite.Email != "invitee@example.com" || issued.Invite.ExpiresAt.Before(time.Now()) {
		t.Fatalf("invite = %+v", issued.Invite)
	}
	if issued.InviteLink == "" || issued.InviteLink[:len("https://wallet.example")] != "https://wallet.example" {
		t.Fatalf("link = %q", issued.InviteLink)
	}

	t.Setenv(frontendURLEnv, "")
	if _, err := svc.InviteMember(context.Background(), InviteInput{
		AccountID: f.accountID, InvitedBy: f.ownerID, Email: "other@example.com", Role: models.AccountRoleUser,
	}); !errors.Is(err, ErrFrontendURLRequired) {
		t.Fatalf("err = %v, want ErrFrontendURLRequired", err)
	}
}
