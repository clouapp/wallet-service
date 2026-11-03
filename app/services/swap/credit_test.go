package swap_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
