package walletrecords_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type addMembers struct {
	walletrecords.MemberStore
	existing    *models.WalletUser
	lookupErr   error
	restoreErr  error
	rolesErr    error
	createErr   error
	created     *models.WalletUser
	restored    uuid.UUID
	rolesSet    string
	lookedUpFor uuid.UUID
}

func (m *addMembers) FindByWalletAndUserIncludeDeleted(_ context.Context, _, userID uuid.UUID) (*models.WalletUser, error) {
	m.lookedUpFor = userID
	return m.existing, m.lookupErr
}

func (m *addMembers) Restore(_ context.Context, id uuid.UUID) error {
	m.restored = id
	return m.restoreErr
}

func (m *addMembers) SetRoles(_ context.Context, _ uuid.UUID, roles string) error {
	m.rolesSet = roles
	return m.rolesErr
}

func (m *addMembers) Create(_ context.Context, member *models.WalletUser) error {
	m.created = member
	return m.createErr
}

type accountMembers struct {
	member *models.AccountUser
	err    error
}

func (a accountMembers) FindMember(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return a.member, a.err
}

func newMemberships(members *addMembers, accounts accountMembers) *walletrecords.Memberships {
	return walletrecords.NewMemberships(walletrecords.MembershipsDeps{
		Wallets:  &walletrecords.Wallets{},
		Members:  walletrecords.NewMembers(members),
		Accounts: accounts,
	})
}

func TestMemberships_AddMember(t *testing.T) {
	account := uuid.New()
	wallet := &models.Wallet{ID: uuid.New(), AccountID: &account}
	user := uuid.New()
	active := accountMembers{member: &models.AccountUser{Status: "active"}}

	t.Run("creates an active membership with the roles", func(t *testing.T) {
		members := &addMembers{lookupErr: models.ErrRepositoryNotFound}

		member, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "viewer,spender")

		require.NoError(t, err)
		assert.Same(t, members.created, member)
		assert.Equal(t, wallet.ID, member.WalletID)
		assert.Equal(t, user, member.UserID)
		assert.Equal(t, "active", member.Status)
		assert.Equal(t, "viewer,spender", member.Roles)
		assert.NotEqual(t, uuid.Nil, member.ID)
	})

	t.Run("refuses roles outside the wallet vocabulary before reading anything", func(t *testing.T) {
		members := &addMembers{}

		_, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "owner")

		assert.ErrorIs(t, err, models.ErrInvalidWalletRoles)
		assert.Equal(t, uuid.Nil, members.lookedUpFor)
	})

	t.Run("refuses a user who is not an active member of the account", func(t *testing.T) {
		noAccount := &models.Wallet{ID: wallet.ID}
		cases := map[string]struct {
			wallet   *models.Wallet
			accounts accountMembers
		}{
			"a wallet without an account": {noAccount, active},
			"an unknown user":             {wallet, accountMembers{err: models.ErrRepositoryNotFound}},
			"no member returned":          {wallet, accountMembers{}},
			"a suspended member":          {wallet, accountMembers{member: &models.AccountUser{Status: "suspended"}}},
		}
		for name, tc := range cases {
			members := &addMembers{}

			_, err := newMemberships(members, tc.accounts).AddMember(context.Background(), tc.wallet, user, "viewer")

			assert.ErrorIs(t, err, walletrecords.ErrNotAccountMember, name)
			assert.Nil(t, members.created, name)
		}
	})

	t.Run("a failed account read is an error that is not a refusal", func(t *testing.T) {
		_, err := newMemberships(&addMembers{}, accountMembers{err: errors.New("pq: down")}).AddMember(context.Background(), wallet, user, "viewer")

		require.Error(t, err)
		assert.NotErrorIs(t, err, walletrecords.ErrNotAccountMember)
	})

	t.Run("a failed membership read creates nothing", func(t *testing.T) {
		members := &addMembers{lookupErr: errors.New("membership store unavailable")}

		_, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "viewer")

		assert.ErrorIs(t, err, walletrecords.ErrMembershipLookup)
		assert.Nil(t, members.created)
	})

	t.Run("a membership on the wallet already is refused and left as it is", func(t *testing.T) {
		current := &models.WalletUser{ID: uuid.New(), Roles: "viewer"}
		members := &addMembers{existing: current}

		_, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "approver")

		assert.ErrorIs(t, err, walletrecords.ErrAlreadyWalletMember)
		assert.Nil(t, members.created)
		assert.Equal(t, uuid.Nil, members.restored)
		assert.Empty(t, members.rolesSet)
	})

	t.Run("a removed membership is restored with the new roles", func(t *testing.T) {
		removedAt := time.Now()
		removed := &models.WalletUser{ID: uuid.New(), Roles: "viewer", DeletedAt: &removedAt}
		members := &addMembers{existing: removed}

		member, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "approver")

		require.NoError(t, err)
		assert.Same(t, removed, member)
		assert.Equal(t, removed.ID, members.restored)
		assert.Equal(t, "approver", members.rolesSet)
		assert.Equal(t, "approver", member.Roles)
		assert.Nil(t, member.DeletedAt, "the answer is the restored membership, not the removed one")
		assert.Nil(t, members.created)
	})

	t.Run("a restore keeps the old roles on the answer when setting them fails", func(t *testing.T) {
		removedAt := time.Now()
		removed := &models.WalletUser{ID: uuid.New(), Roles: "viewer", DeletedAt: &removedAt}
		members := &addMembers{existing: removed, rolesErr: errors.New("pq: down")}

		member, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "approver")

		require.NoError(t, err)
		assert.Equal(t, "viewer", member.Roles)
	})

	t.Run("a failed restore is marked", func(t *testing.T) {
		removedAt := time.Now()
		members := &addMembers{existing: &models.WalletUser{ID: uuid.New(), DeletedAt: &removedAt}, restoreErr: errors.New("pq: down")}

		_, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "viewer")

		assert.ErrorIs(t, err, walletrecords.ErrMembershipRestore)
	})

	t.Run("a failed create is an error that is not a refusal", func(t *testing.T) {
		members := &addMembers{lookupErr: models.ErrRepositoryNotFound, createErr: errors.New("pq: down")}

		_, err := newMemberships(members, active).AddMember(context.Background(), wallet, user, "viewer")

		require.Error(t, err)
		assert.NotErrorIs(t, err, walletrecords.ErrMembershipLookup)
	})
}
