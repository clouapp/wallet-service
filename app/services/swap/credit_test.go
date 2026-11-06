package swap_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCreditVerifier_ExcludesAnotherWalletAddressAssetAndUnconfirmedDeposits(t *testing.T) {
	walletID := uuid.New()
	asset := swap.Asset{Symbol: "USDC", Chain: "eth", NetworkID: 1, Contract: "0x1111111111111111111111111111111111111111", Decimals: 6}
	base := models.Transaction{WalletID: walletID, ToAddress: "0x2222222222222222222222222222222222222222", Asset: "usdc", TokenContract: asset.Contract, Amount: "1000001", Status: "confirmed"}
	for _, tc := range []struct {
		name     string
		change   func(*models.Transaction)
		credited bool
	}{{"matching finalized deposit", func(*models.Transaction) {}, true}, {"another wallet", func(tx *models.Transaction) { tx.WalletID = uuid.New() }, false}, {"another recipient", func(tx *models.Transaction) { tx.ToAddress = "0xother" }, false}, {"another token contract", func(tx *models.Transaction) { tx.TokenContract = "0xother" }, false}, {"not finalized", func(tx *models.Transaction) { tx.Status = "confirming" }, false}} {
		t.Run(tc.name, func(t *testing.T) {
			deposits := mocks.NewMockSwapDeposits(t)
			claims := mocks.NewMockSwapCreditClaims(t)
			row := base
			tc.change(&row)
			deposits.On("FindDepositsByChainAndTxHash", mock.Anything, "eth", "receiving-hash").Return([]models.Transaction{row}, nil).Once()
			claims.On("ClaimCredit", mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, allocation *models.SwapCreditAllocation, total func(context.Context) (string, error)) (string, bool, error) {
				raw, err := total(ctx)
				if err != nil {
					return "0", false, err
				}
				available, ok := amount.ParseBaseUnits(raw)
				require.True(t, ok)
				requested, ok := amount.ParseBaseUnits(allocation.AmountRaw)
				require.True(t, ok)
				if available.Cmp(requested) < 0 {
					return "0", false, nil
				}
				return allocation.AmountRaw, true, nil
			}).Once()
			amount, confirmed, err := swap.NewDepositVerifier(deposits, claims).Credit(context.Background(), swap.CreditRequest{OrderID: uuid.New(), WalletID: walletID, Direction: "receive", Asset: asset, Address: base.ToAddress, Hash: "receiving-hash", ReportedRaw: "1000001"})
			require.NoError(t, err)
			require.Equal(t, tc.credited, confirmed)
			if tc.credited {
				require.Equal(t, "1000001", amount)
			} else {
				require.Equal(t, "0", amount)
			}
		})
	}
}

func TestCreditVerifier_IgnoresDepositsRecordedBeforeTheOrder(t *testing.T) {
	walletID := uuid.New()
	asset := swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}
	orderCreated := time.Now().Add(-time.Hour)
	for name, recorded := range map[string]time.Time{"before the order": orderCreated.Add(-time.Minute), "after the order": orderCreated.Add(time.Minute)} {
		t.Run(name, func(t *testing.T) {
			row := models.Transaction{WalletID: walletID, ToAddress: "0x2222222222222222222222222222222222222222", Asset: "eth", Amount: "500", Status: "confirmed"}
			row.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(recorded))
			deposits := mocks.NewMockSwapDeposits(t)
			deposits.On("FindDepositsByChainAndTxHash", mock.Anything, "eth", mock.Anything).Return([]models.Transaction{row}, nil).Once()
			claims := mocks.NewMockSwapCreditClaims(t)
			var total string
			claims.On("ClaimCredit", mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, _ *models.SwapCreditAllocation, read func(context.Context) (string, error)) (string, bool, error) {
				var err error
				total, err = read(ctx)
				return total, total != "0", err
			}).Once()
			_, confirmed, err := swap.NewDepositVerifier(deposits, claims).Credit(context.Background(), swap.CreditRequest{OrderID: uuid.New(), WalletID: walletID, Direction: "refund", Asset: asset, Address: row.ToAddress, Hash: "0xpayout", ReportedRaw: "500", NotBefore: orderCreated})
			require.NoError(t, err)
			require.Equal(t, recorded.After(orderCreated), confirmed)
		})
	}
}

func TestCreditVerifier_SpellsAHexPayoutHashOneWay(t *testing.T) {
	hash := "0X" + strings.Repeat("AB", 32)
	deposits := mocks.NewMockSwapDeposits(t)
	deposits.On("FindDepositsByChainAndTxHash", mock.Anything, "eth", mock.Anything).Return([]models.Transaction{}, nil).Maybe()
	claims := mocks.NewMockSwapCreditClaims(t)
	claims.On("ClaimCredit", mock.Anything, mock.MatchedBy(func(a *models.SwapCreditAllocation) bool {
		return a.TxHash == "0x"+strings.Repeat("ab", 32)
	}), mock.Anything).Return("0", false, nil).Once()
	_, _, err := swap.NewDepositVerifier(deposits, claims).Credit(context.Background(), swap.CreditRequest{OrderID: uuid.New(), WalletID: uuid.New(), Direction: "receive", Asset: swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Native: true}, Address: "0x2222222222222222222222222222222222222222", Hash: hash, ReportedRaw: "1"})
	require.NoError(t, err)
}

func TestCreditVerifier_SpellsAnUnprefixedHexPayoutHashOneWay(t *testing.T) {
	for _, hash := range []string{strings.Repeat("AB", 32), strings.Repeat("ab", 32)} {
		deposits := mocks.NewMockSwapDeposits(t)
		claims := mocks.NewMockSwapCreditClaims(t)
		claims.On("ClaimCredit", mock.Anything, mock.MatchedBy(func(a *models.SwapCreditAllocation) bool {
			return a.TxHash == strings.Repeat("ab", 32)
		}), mock.Anything).Return("0", false, nil).Once()
		_, _, err := swap.NewDepositVerifier(deposits, claims).Credit(context.Background(), swap.CreditRequest{OrderID: uuid.New(), WalletID: uuid.New(), Direction: "receive", Asset: swap.Asset{Symbol: "BTC", Chain: "btc", Decimals: 8, Native: true}, Address: "bc1qdest", Hash: hash, ReportedRaw: "1"})
		require.NoError(t, err)
	}
}
