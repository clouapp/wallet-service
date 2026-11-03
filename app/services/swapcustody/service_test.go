package swapcustody_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/app/services/swapcustody"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/memcache"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSend_PersistsBeforeBroadcastAndNeverDropsAnUnknownHash(t *testing.T) {
	for _, persistFailure := range []bool{true, false} {
		t.Run(map[bool]string{true: "database failure prevents send", false: "unknown outcome keeps signed hash"}[persistFailure], func(t *testing.T) {
			steps := mocks.NewMockSwapSteps(t)
			registry := mocks.NewMockSwapRegistry(t)
			signer := mocks.NewMockPreparedSigner(t)
			authority := mocks.NewMockSwapAuthority(t)
			accountID := uuid.New()
			wallet := &models.Wallet{ID: uuid.New(), AccountID: &accountID, Chain: "btc", DepositAddress: &models.Address{ID: uuid.New(), Address: "source"}}
			order := &models.SwapOrder{ID: uuid.New(), SourceWalletID: wallet.ID}
			quote := &swap.Quote{ExpiresAt: time.Now().Add(time.Minute), Request: swap.QuoteRequest{From: swap.Asset{Symbol: "BTC", Chain: "btc", Decimals: 8, Native: true}, FromAddress: "source", AmountRaw: "10000"}}
			funding := &swap.Funding{Kind: "transfer", To: "deposit", AmountRaw: "10000"}
			adapter := mocks.NewMockChain("btc")
			adapter.GetBalanceFn = func(context.Context, string) (*types.Balance, error) {
				return &types.Balance{Amount: big.NewInt(50000)}, nil
			}
			unsigned := &types.UnsignedTx{ChainID: "btc", Metadata: map[string]any{"inputs": "fixture"}}
			adapter.BuildTransferFn = func(_ context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
				require.Equal(t, "source", req.From)
				require.Equal(t, "deposit", req.To)
				require.Equal(t, "10000", req.Amount.String())
				return unsigned, nil
			}
			signed := &types.SignedTx{ChainID: "btc", TxHash: "hash-of-exact-signed-bytes", RawBytes: []byte{1, 2, 3}}
			persisted := false
			adapter.BroadcastTransactionFn = func(_ context.Context, tx *types.SignedTx) (string, error) {
				require.True(t, persisted, "signed transaction must already be durable")
				require.Same(t, signed, tx)
				return "", chain.ErrUnknownOutcome
			}
			steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{}, nil).Once()
			registry.On("ChainForWallet", wallet).Return(adapter, nil).Once()
			authority.On("ReserveSwapSpend", mock.Anything, wallet, quote, mock.Anything).Return(nil).Once()
			signer.On("SignPrepared", mock.Anything, wallet.ID, "test-only-passphrase", adapter, unsigned).Return(signed, nil).Once()
			signer.On("PrepareBroadcast", mock.Anything, adapter, signed).Return(`{"signed_tx":"enc:v1:fixture"}`, nil).Once()
			var saveErr error
			if persistFailure {
				saveErr = errors.New("database unavailable")
			}
			steps.On("RecordSigned", mock.Anything, mock.Anything, mock.MatchedBy(func(tx *models.Transaction) bool {
				return tx.TxHash == signed.TxHash && tx.RawPayload == `{"signed_tx":"enc:v1:fixture"}` && tx.WalletID == wallet.ID
			})).Run(func(mock.Arguments) { persisted = !persistFailure }).Return(saveErr).Once()
			cache := memcache.New()
			service := swapcustody.New(swapcustody.Deps{Authority: authority, Registry: registry, Steps: steps, Signer: signer, Cache: cache})
			result, err := service.Send(context.Background(), order, wallet, quote, funding, swap.ExecuteInput{Passphrase: "test-only-passphrase"})
			if persistFailure {
				require.ErrorIs(t, err, swap.ErrExecutionFailed)
				require.Nil(t, result)
				require.Zero(t, adapter.BroadcastTransactionCalls)
			} else {
				require.ErrorIs(t, err, chain.ErrUnknownOutcome)
				require.Equal(t, signed.TxHash, result.Hash)
				require.True(t, result.Uncertain)
				require.Equal(t, 1, adapter.BroadcastTransactionCalls)
			}
			require.False(t, cache.Has("vault:lock:withdrawal:"+wallet.ID.String()))
		})
	}
}

func TestSend_DurableFundingIsReplayedWithoutBuildingOrSigning(t *testing.T) {
	steps := mocks.NewMockSwapSteps(t)
	order := &models.SwapOrder{ID: uuid.New()}
	wallet := &models.Wallet{ID: uuid.New()}
	steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{{Purpose: "funding", TxHash: "already-signed"}}, nil).Once()
	service := swapcustody.New(swapcustody.Deps{Steps: steps, Signer: mocks.NewMockPreparedSigner(t), Cache: memcache.New()})
	result, err := service.Send(context.Background(), order, wallet, nil, nil, swap.ExecuteInput{})
	require.NoError(t, err)
	require.Equal(t, "already-signed", result.Hash)
	require.True(t, result.Uncertain)
}

func TestSend_ApprovalPendingDoesNotSpendTheSwapInput(t *testing.T) {
	steps := mocks.NewMockSwapSteps(t)
	transactions := mocks.NewMockSwapTransactions(t)
	order := &models.SwapOrder{ID: uuid.New()}
	wallet := &models.Wallet{ID: uuid.New()}
	txID := uuid.New()
	steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{{Purpose: "approval", TransactionID: txID}}, nil).Once()
	transactions.On("FindByID", mock.Anything, txID).Return(&models.Transaction{Status: string(types.TxStatusConfirming)}, nil).Once()
	service := swapcustody.New(swapcustody.Deps{Steps: steps, Transactions: transactions, Signer: mocks.NewMockPreparedSigner(t), Cache: memcache.New()})
	result, err := service.Send(context.Background(), order, wallet, nil, nil, swap.ExecuteInput{})
	require.NoError(t, err)
	require.True(t, result.Pending)
	require.Empty(t, result.Hash)
}
