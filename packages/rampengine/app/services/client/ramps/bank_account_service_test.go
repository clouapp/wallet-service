package ramps_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
	"github.com/macrowallets/waas/packages/rampengine/app/support/usbank"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func newBankAccountService(t *testing.T) (*ramps.BankAccountService, *mocks.MockUserBankAccountRepository, *mocks.MockMfaStepUpValidator) {
	svc, accounts, mfaStepUp, _ := newBankAccountServiceWithPayouts(t)
	return svc, accounts, mfaStepUp
}

func newBankAccountServiceWithPayouts(t *testing.T) (
	*ramps.BankAccountService, *mocks.MockUserBankAccountRepository, *mocks.MockMfaStepUpValidator, *mocks.MockBankAccountPayouts,
) {
	t.Helper()
	// The account number's digest is keyed by APP_KEY. Another test in the
	// package replaces the framework's app with a mock, so the key is stated
	// here instead of read from whatever answered last.
	mock.Factory().Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()
	accounts := mocks.NewMockUserBankAccountRepository(t)
	mfaStepUp := mocks.NewMockMfaStepUpValidator(t)
	payouts := mocks.NewMockBankAccountPayouts(t)
	return ramps.NewBankAccountService(accounts, payouts, mfaStepUp, passthroughLocker{}), accounts, mfaStepUp, payouts
}

// sequenceLocker runs fn and notes the key it held, so a test can tell
// which writes happened under which lock.
type sequenceLocker struct{ held *[]string }

func (l sequenceLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) error {
	*l.held = append(*l.held, "lock:"+key)
	defer func() { *l.held = append(*l.held, "unlock:"+key) }()
	return fn(ctx)
}

// createBankAccountInput is an account as a person types it: spaces in the
// numbers and the name, lowercase state, uppercase type.
func createBankAccountInput() dto.CreateBankAccountDTO {
	return dto.CreateBankAccountDTO{
		Label: " Minha conta corrente ", OwnerName: " Leandro   Henriques Teste ", BankName: " Chase ",
		RoutingNumber: "021 000 021", AccountNumber: "0001 2345 6789", AccountType: "Checking",
		Address: models.BankAccountAddress{
			StreetLine1: " 123 Market St ", City: " San Francisco ", State: "ca", PostalCode: "94103", Country: "usa",
		},
	}
}

func savedView(id uint) *repositories.UserBankAccountView {
	return &repositories.UserBankAccountView{
		ID: id, Label: "Minha conta corrente", OwnerName: "Leandro Henriques Teste", BankName: "Chase",
		RoutingNumber: "021000021", AccountNumber: "000123456789", RoutingLast4: "0021", AccountLast4: "6789",
		AccountType: "checking",
		Address: models.BankAccountAddress{StreetLine1: "123 Market St", City: "San Francisco", State: "CA",
			PostalCode: "94103", Country: "USA"},
	}
}

// What reaches the repository is normalized once, here: digits, collapsed
// spaces, capitals where Bridge wants them, and the hash of the digits.
func TestBankAccountService_Create_Normalizes(t *testing.T) {
	svc, accounts, mfaStepUp := newBankAccountService(t)
	mfaStepUp.EXPECT().Enrolled(user).Return(false, nil).Once()
	var got repositories.UserBankAccountInput
	accounts.EXPECT().Create(userID, testifymock.Anything).RunAndReturn(
		func(_ uint, in repositories.UserBankAccountInput) (*repositories.UserBankAccountView, error) {
			got = in
			return savedView(12), nil
		}).Once()

	view, err := svc.Create(context.Background(), user, createBankAccountInput())
	require.NoError(t, err)

	require.Equal(t, "Minha conta corrente", got.Label)
	require.Equal(t, "Leandro Henriques Teste", got.OwnerName)
	require.Equal(t, "Chase", got.BankName)
	require.Equal(t, "021000021", got.RoutingNumber)
	require.Equal(t, "000123456789", got.AccountNumber)
	require.Equal(t, "checking", got.AccountType)
	require.Equal(t, usbank.AccountHash("021000021", "000123456789"), got.AccountHash)
	require.Equal(t, "CA", got.Address.State)
	require.Equal(t, "USA", got.Address.Country)
	require.Equal(t, "123 Market St", got.Address.StreetLine1)

	require.Equal(t, uint(12), view.ID)
	require.Equal(t, "6789", view.AccountLast4)
	require.Equal(t, "0021", view.RoutingLast4)
}

// A new payout destination is behind the step-up: with MFA enrolled and no
// code, nothing is saved.
func TestBankAccountService_Create_RequiresTheStepUp(t *testing.T) {
	svc, _, mfaStepUp := newBankAccountService(t)
	mfaStepUp.EXPECT().Enrolled(user).Return(true, nil).Once()

	_, err := svc.Create(context.Background(), user, createBankAccountInput())
	require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
}

func TestBankAccountService_Create_RefusesAWrongCode(t *testing.T) {
	svc, _, mfaStepUp := newBankAccountService(t)
	mfaStepUp.EXPECT().Enrolled(user).Return(true, nil).Once()
	mfaStepUp.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "000000", "totp").Return(mfa.ErrInvalidCode).Once()
	in := createBankAccountInput()
	in.Code, in.CodeType = "000000", "totp"

	_, err := svc.Create(context.Background(), user, in)
	require.ErrorIs(t, err, mfa.ErrInvalidCode)
}

func TestBankAccountService_Create_AcceptsTheRightCode(t *testing.T) {
	svc, accounts, mfaStepUp := newBankAccountService(t)
	mfaStepUp.EXPECT().Enrolled(user).Return(true, nil).Once()
	mfaStepUp.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "123456", "totp").Return(nil).Once()
	accounts.EXPECT().Create(userID, testifymock.Anything).Return(savedView(12), nil).Once()
	in := createBankAccountInput()
	in.Code, in.CodeType = "123456", "totp"

	_, err := svc.Create(context.Background(), user, in)
	require.NoError(t, err)
}

func TestBankAccountService_Create_ReportsADuplicate(t *testing.T) {
	svc, accounts, mfaStepUp := newBankAccountService(t)
	mfaStepUp.EXPECT().Enrolled(user).Return(false, nil).Once()
	accounts.EXPECT().Create(userID, testifymock.Anything).Return(nil, repositories.ErrDuplicateBankAccount).Once()

	_, err := svc.Create(context.Background(), user, createBankAccountInput())
	require.ErrorIs(t, err, ramps.ErrDuplicateBankAccount)
}

func TestBankAccountService_List(t *testing.T) {
	svc, accounts, _ := newBankAccountService(t)
	accounts.EXPECT().ListByUser(userID).Return([]repositories.UserBankAccountView{*savedView(1), *savedView(2)}, nil).Once()

	views, err := svc.List(userID)
	require.NoError(t, err)
	require.Len(t, views, 2)
	require.Equal(t, "6789", views[1].AccountLast4)
}

func TestBankAccountService_UpdateLabelAndDeleteReportAMiss(t *testing.T) {
	svc, accounts, _, payouts := newBankAccountServiceWithPayouts(t)
	accounts.EXPECT().UpdateLabel(userID, uint(9), "new").Return(nil, repositories.ErrBankAccountNotFound).Once()
	// Scoped to the user, so another user's account deactivates nothing.
	payouts.EXPECT().DeactivateBankAccountAddresses(userID, uint(9)).Return(nil).Once()
	accounts.EXPECT().Delete(userID, uint(9)).Return(repositories.ErrBankAccountNotFound).Once()

	_, err := svc.UpdateLabel(userID, 9, " new ")
	require.ErrorIs(t, err, ramps.ErrBankAccountNotFound)
	require.ErrorIs(t, svc.Delete(context.Background(), userID, 9), ramps.ErrBankAccountNotFound)
}

// Deleting an account stops its payout addresses: they go inactive here before
// the account is deleted (so none is offered again), and the provider is told
// to stop paying it once the delete holds.
//
// The two local writes run under the payout address lock a provision takes,
// so a provision cannot reserve between them; the provider call runs after.
func TestBankAccountService_DeleteStopsThePayoutAddresses(t *testing.T) {
	_, accounts, mfaStepUp, payouts := newBankAccountServiceWithPayouts(t)
	var order []string
	svc := ramps.NewBankAccountService(accounts, payouts, mfaStepUp, sequenceLocker{held: &order})
	payouts.EXPECT().DeactivateBankAccountAddresses(userID, uint(9)).RunAndReturn(func(uint, uint) error {
		order = append(order, "addresses")
		return nil
	}).Once()
	accounts.EXPECT().Delete(userID, uint(9)).RunAndReturn(func(uint, uint) error {
		order = append(order, "account")
		return nil
	}).Once()
	payouts.EXPECT().DeactivateBankAccountUpstream(testifymock.Anything, uint(9)).Run(func(context.Context, uint) {
		order = append(order, "upstream")
	}).Once()

	require.NoError(t, svc.Delete(context.Background(), userID, 9))
	key := ramps.PayoutAddressLockKey(userID)
	require.Equal(t, []string{"lock:" + key, "addresses", "account", "unlock:" + key, "upstream"}, order)
}

// An address that cannot be stopped keeps the account: deleting it would leave
// an active address paying an account the user no longer sees.
func TestBankAccountService_DeleteKeepsTheAccountWhenTheAddressesCannotBeStopped(t *testing.T) {
	svc, _, _, payouts := newBankAccountServiceWithPayouts(t)
	payouts.EXPECT().DeactivateBankAccountAddresses(userID, uint(9)).Return(errors.New("db down")).Once()

	require.Error(t, svc.Delete(context.Background(), userID, 9))
}
