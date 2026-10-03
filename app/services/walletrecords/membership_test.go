package walletrecords_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func TestMembershipsForWalletReturnsTheStoredRoles(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	userID := uuid.New()
	walletID := uuid.New()
	wallets := &roleWallets{wallet: &models.Wallet{ID: walletID, AccountID: &accountID}}
	members := &roleMembers{member: &models.WalletUser{Roles: "admin"}}
	accounts := &roleAccounts{member: &models.AccountUser{Role: "owner"}}
	loader := walletrecords.NewMemberships(walletrecords.NewWallets(wallets), walletrecords.NewMembers(members), accounts)

	walletRole, accountRole := loader.ForWallet(context.Background(), walletID, userID)
	require.Equal(t, "admin", walletRole)
	require.Equal(t, "owner", accountRole)
	require.Equal(t, walletID, members.walletID)
	require.Equal(t, userID, members.userID)
	require.Equal(t, accountID, accounts.accountID)
}

func TestMembershipsForWalletTreatsAMissAsAnEmptyRole(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	userID := uuid.New()
	walletID := uuid.New()
	lookupErr := errors.New("missing")

	walletRole, accountRole := walletrecords.NewMemberships(
		walletrecords.NewWallets(&roleWallets{err: lookupErr}),
		walletrecords.NewMembers(&roleMembers{member: &models.WalletUser{Roles: "owner"}}),
		&roleAccounts{},
	).ForWallet(context.Background(), walletID, userID)
	require.Equal(t, "owner", walletRole)
	require.Equal(t, "", accountRole)

	walletRole, accountRole = walletrecords.NewMemberships(
		walletrecords.NewWallets(&roleWallets{wallet: &models.Wallet{ID: walletID}}),
		walletrecords.NewMembers(&roleMembers{err: lookupErr}),
		&roleAccounts{member: &models.AccountUser{Role: "admin"}},
	).ForWallet(context.Background(), walletID, userID)
	require.Equal(t, "", walletRole)
	require.Equal(t, "", accountRole)

	walletRole, accountRole = walletrecords.NewMemberships(
		walletrecords.NewWallets(&roleWallets{wallet: &models.Wallet{ID: walletID, AccountID: &accountID}}),
		walletrecords.NewMembers(&roleMembers{member: &models.WalletUser{Roles: "viewer"}}),
		&roleAccounts{err: lookupErr},
	).ForWallet(context.Background(), walletID, userID)
	require.Equal(t, "viewer", walletRole)
	require.Equal(t, "", accountRole)
}

func TestNewMembershipsRejectsAMissingDependency(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		walletrecords.NewMemberships(nil, walletrecords.NewMembers(&roleMembers{}), &roleAccounts{})
	})
}

type roleWallets struct {
	wallet *models.Wallet
	err    error
}

func (f *roleWallets) PaginateByAccount(context.Context, uuid.UUID, string, int, int) ([]models.Wallet, int64, error) {
	return nil, 0, f.err
}
func (f *roleWallets) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return f.wallet, f.err
}
func (f *roleWallets) FindByIDAndAccount(context.Context, uuid.UUID, uuid.UUID) (*models.Wallet, error) {
	return f.wallet, f.err
}
func (f *roleWallets) SetFeeRateMin(context.Context, uuid.UUID, int) error { return f.err }
func (f *roleWallets) SetFeeRateMax(context.Context, uuid.UUID, int) error { return f.err }
func (f *roleWallets) SetFeeMultiplier(context.Context, uuid.UUID, float64) error {
	return f.err
}
func (f *roleWallets) SetRequiredApprovals(context.Context, uuid.UUID, int) error { return f.err }
func (f *roleWallets) SetFrozenUntil(context.Context, uuid.UUID, time.Time) error { return f.err }
func (f *roleWallets) SetStatus(context.Context, uuid.UUID, string) error         { return f.err }
func (f *roleWallets) SetLabel(context.Context, uuid.UUID, string) error          { return f.err }

type roleMembers struct {
	member   *models.WalletUser
	err      error
	walletID uuid.UUID
	userID   uuid.UUID
}

func (f *roleMembers) FindByWalletID(context.Context, uuid.UUID) ([]models.WalletUser, error) {
	return nil, f.err
}
func (f *roleMembers) FindByWalletAndUser(_ context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error) {
	f.walletID = walletID
	f.userID = userID
	return f.member, f.err
}
func (f *roleMembers) FindByWalletAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.WalletUser, error) {
	return f.member, f.err
}
func (f *roleMembers) Restore(context.Context, uuid.UUID) error               { return f.err }
func (f *roleMembers) SetRoles(context.Context, uuid.UUID, string) error      { return f.err }
func (f *roleMembers) Create(context.Context, *models.WalletUser) error       { return f.err }
func (f *roleMembers) SoftDelete(context.Context, uuid.UUID, uuid.UUID) error { return f.err }

type roleAccounts struct {
	member    *models.AccountUser
	err       error
	accountID uuid.UUID
}

func (f *roleAccounts) FindMember(_ context.Context, accountID, _ uuid.UUID) (*models.AccountUser, error) {
	f.accountID = accountID
	return f.member, f.err
}
