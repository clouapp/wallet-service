package ramps_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

func settledUSDPayout(bankAccountID uint) *models.Transaction {
	rail, paymentRail := models.RailUSDACHPayout, "ach"
	return &models.Transaction{
		Model: orm.Model{ID: 1}, UserID: userID, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: orderID, GatewayOrderID: "drn_1",
		Status: models.TxStatusCompleted, Rail: &rail, FiatCurrency: models.FiatCurrencyUSD,
		PaymentRail: &paymentRail, BankAccountID: &bankAccountID,
		AmountFiat: "24.75", AmountToken: "25.00", TargetToken: "USDC",
		DepositMode: support.Ptr(ramps.DepositModeOnChain),
	}
}

// A USD payout names its currency, the bank network and the account it paid,
// by label and last four digits — even after the account was deleted.
func TestAUSDPayoutNamesItsCurrencyRailAndAccount(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	accounts := mocks.NewMockUserBankAccountRepository(t)
	d := h.deps
	d.BankAccounts = accounts
	transactions := ramps.NewTransactionService(d)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(settledUSDPayout(12), nil).Once()
	accounts.EXPECT().FindByIDWithTrashed(uint(12)).Return(&repositories.UserBankAccountView{
		ID: 12, UserID: userID, Label: "Conta Chase", AccountLast4: "6789", DeletedAt: support.Ptr(time.Now()),
	}, nil).Once()

	view, err := transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "usd", view.FiatCurrency)
	require.Equal(t, "24.75", view.AmountFiat)
	require.Empty(t, view.AmountBRL, "amount_brl names reais only; a USD order leaves it out")
	require.Equal(t, "ach", view.PaymentRail)
	require.Equal(t, "Conta Chase", view.BankAccountLabel)
	require.Equal(t, "6789", view.BankAccountLast4)
}

// A lookup that fails leaves the account blank; the order is still the answer.
func TestAnUnreadableBankAccountLeavesTheOrderStanding(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	accounts := mocks.NewMockUserBankAccountRepository(t)
	d := h.deps
	d.BankAccounts = accounts
	transactions := ramps.NewTransactionService(d)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(settledUSDPayout(12), nil).Once()
	accounts.EXPECT().FindByIDWithTrashed(uint(12)).Return(nil, errors.New("pq: connection reset")).Once()

	view, err := transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "usd", view.FiatCurrency)
	require.Empty(t, view.BankAccountLabel)
}

// An account id another user's account holds names nothing on this order:
// only the order's own user's account is shown.
func TestAUSDPayoutNamesNoAccountOfAnotherUser(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	accounts := mocks.NewMockUserBankAccountRepository(t)
	d := h.deps
	d.BankAccounts = accounts
	transactions := ramps.NewTransactionService(d)

	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(settledUSDPayout(12), nil).Once()
	accounts.EXPECT().FindByIDWithTrashed(uint(12)).Return(&repositories.UserBankAccountView{
		ID: 12, UserID: userID + 1, Label: "Not yours", AccountLast4: "0000",
	}, nil).Once()

	view, err := transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Empty(t, view.BankAccountLabel)
	require.Empty(t, view.BankAccountLast4)
}

// A Pix order built before fiat_currency existed reads as BRL, and asks for
// no bank account.
func TestAPixOrderIsBRL(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "brl", view.FiatCurrency)
	require.Equal(t, "100.00", view.AmountFiat)
	require.Empty(t, view.PaymentRail)
}
