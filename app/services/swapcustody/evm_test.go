package swapcustody_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/app/services/swapcustody"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/memcache"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type preparedSwapCall struct {
	to    string
	value *big.Int
	data  []byte
}

type swapEVMChain struct {
	*mocks.MockChain
	allowance    *big.Int
	standing     map[string]*big.Int
	calls        []preparedSwapCall
	prepareError error
}

// SwapAllowance is the source token's allowance, or a standing allowance of
// another catalog token (none unless a test sets one).
func (c *swapEVMChain) SwapAllowance(_ context.Context, token, _, _ string) (*big.Int, error) {
	if strings.EqualFold(token, sourceTokenContract) {
		return new(big.Int).Set(c.allowance), nil
	}
	if standing := c.standing[strings.ToLower(token)]; standing != nil {
		return new(big.Int).Set(standing), nil
	}
	return new(big.Int), nil
}

func (c *swapEVMChain) PrepareSwapCall(_ context.Context, _ string, to string, value *big.Int, data []byte) (*types.UnsignedTx, error) {
	c.calls = append(c.calls, preparedSwapCall{to: to, value: new(big.Int).Set(value), data: append([]byte(nil), data...)})
	return &types.UnsignedTx{ChainID: "eth", Metadata: map[string]any{"to": to}}, c.prepareError
}

// tokenCatalog is the custody token list the guard reads.
type tokenCatalog []types.Token

func (c tokenCatalog) TokensForChain(string) []types.Token { return c }

const (
	sourceTokenContract = "0x1111111111111111111111111111111111111111"
	otherTokenContract  = "0x6666666666666666666666666666666666666666"
	routerContract      = "0x4444444444444444444444444444444444444444"
)

type swapEVMFixture struct {
	deps                       swapcustody.Deps
	service                    *swapcustody.Service
	adapter                    *swapEVMChain
	wallet                     *models.Wallet
	order                      *models.SwapOrder
	quote                      *swap.Quote
	funding                    *swap.Funding
	steps                      []models.SwapExecutionStep
	transactions               map[uuid.UUID]*models.Transaction
	included                   map[string]bool
	signs, sends, reservations int
}

func newSwapEVMFixture(t *testing.T, token bool) *swapEVMFixture {
	t.Helper()
	f := &swapEVMFixture{transactions: map[uuid.UUID]*models.Transaction{}, included: map[string]bool{}}
	f.wallet = &models.Wallet{ID: uuid.New(), Chain: "eth", DepositAddress: &models.Address{ID: uuid.New(), Address: "0x2222222222222222222222222222222222222222"}}
	f.order = &models.SwapOrder{ID: uuid.New(), SourceWalletID: f.wallet.ID}
	asset := swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}
	if token {
		asset = swap.Asset{Symbol: "USDT", Chain: "eth", NetworkID: 1, Decimals: 6, Contract: sourceTokenContract}
	}
	f.quote = &swap.Quote{ID: uuid.New(), ExpiresAt: time.Now().Add(time.Minute), Request: swap.QuoteRequest{From: asset, FromAddress: f.wallet.DepositAddress.Address, AmountRaw: "100"}}
	value := "100"
	if token {
		value = "0"
	}
	f.funding = &swap.Funding{Kind: "evm", ChainID: 1, To: routerContract, AmountRaw: value, Data: "0x1234", ApprovalAddress: routerContract}
	f.adapter = &swapEVMChain{MockChain: mocks.NewMockChain("eth"), allowance: big.NewInt(200), standing: map[string]*big.Int{}}
	if !token {
		// A native-source wallet has granted nothing on the catalog's tokens.
		f.adapter.allowance = new(big.Int)
	}
	f.adapter.GetBalanceFn = func(context.Context, string) (*types.Balance, error) {
		return &types.Balance{Amount: big.NewInt(1000)}, nil
	}
	f.adapter.GetTokenBalanceFn = func(_ context.Context, _ string, token types.Token) (*types.Balance, error) {
		if strings.EqualFold(token.Contract, sourceTokenContract) {
			return &types.Balance{Amount: big.NewInt(1000)}, nil
		}
		return &types.Balance{Amount: new(big.Int)}, nil
	}
	f.adapter.GetTransactionBlockFn = func(_ context.Context, hash string) (uint64, error) {
		if f.included[hash] {
			return 1, nil
		}
		return 0, nil
	}
	f.adapter.BroadcastTransactionFn = func(_ context.Context, tx *types.SignedTx) (string, error) {
		require.Equal(t, len(f.steps), f.sends+1, "each signed step must be durable before broadcast")
		require.Equal(t, tx.TxHash, f.steps[len(f.steps)-1].TxHash)
		call := f.adapter.calls[len(f.adapter.calls)-1]
		if len(call.data) == 68 && hex.EncodeToString(call.data[:4]) == "095ea7b3" {
			if strings.EqualFold(call.to, sourceTokenContract) {
				f.adapter.allowance.SetBytes(call.data[36:])
			} else {
				f.adapter.standing[strings.ToLower(call.to)] = new(big.Int).SetBytes(call.data[36:])
			}
		}
		f.sends++
		return tx.TxHash, nil
	}
	steps := mocks.NewMockSwapSteps(t)
	steps.On("ExecutionSteps", mock.Anything, f.order.ID).Return(func(context.Context, uuid.UUID) []models.SwapExecutionStep {
		return append([]models.SwapExecutionStep(nil), f.steps...)
	}, nil)
	steps.On("RecordSigned", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		step := args.Get(1).(*models.SwapExecutionStep)
		tx := args.Get(2).(*models.Transaction)
		f.steps = append(f.steps, *step)
		copyTx := *tx
		f.transactions[tx.ID] = &copyTx
	}).Return(nil).Maybe()
	transactions := mocks.NewMockSwapTransactions(t)
	transactions.On("FindByID", mock.Anything, mock.Anything).Return(func(_ context.Context, id uuid.UUID) *models.Transaction { return f.transactions[id] }, nil).Maybe()
	registry := mocks.NewMockSwapRegistry(t)
	registry.On("ChainForWallet", f.wallet).Return(f.adapter, nil)
	authority := mocks.NewMockSwapAuthority(t)
	authority.On("ReserveSwapSpend", mock.Anything, f.wallet, f.quote, mock.Anything).Run(func(mock.Arguments) { f.reservations++ }).Return(nil).Maybe()
	signer := mocks.NewMockPreparedSigner(t)
	signer.On("SignPrepared", mock.Anything, f.wallet.ID, "test-only-passphrase", f.adapter, mock.Anything).Return(func(context.Context, uuid.UUID, string, types.Chain, *types.UnsignedTx) *types.SignedTx {
		f.signs++
		return &types.SignedTx{ChainID: "eth", TxHash: fmt.Sprintf("signed-step-%d", f.signs), RawBytes: []byte{byte(f.signs)}}
	}, nil).Maybe()
	signer.On("PrepareBroadcast", mock.Anything, f.adapter, mock.Anything).Return(`{"signed_tx":"enc:v1:test-fixture"}`, nil).Maybe()
	catalog := tokenCatalog{{Symbol: "USDT", Contract: sourceTokenContract, Decimals: 6}, {Symbol: "USDC", Contract: otherTokenContract, Decimals: 6}}
	f.deps = swapcustody.Deps{Authority: authority, Registry: registry, Steps: steps, Transactions: transactions, Signer: signer, Cache: memcache.New(), Tokens: catalog}
	f.service = swapcustody.New(f.deps)
	return f
}

// waitingForInclusion rebuilds the service so one Send waits for approvals.
func (f *swapEVMFixture) waitingForInclusion(wait time.Duration) {
	f.deps.InclusionWait, f.deps.PollInterval = wait, 5*time.Millisecond
	f.service = swapcustody.New(f.deps)
}

func (f *swapEVMFixture) send() (*swap.SendResult, error) {
	return f.service.Send(context.Background(), f.order, f.wallet, f.quote, f.funding, swap.ExecuteInput{Passphrase: "test-only-passphrase"})
}

func TestSend_EVMNativeValueMustEqualTheAcceptedInput(t *testing.T) {
	for _, value := range []string{"0", "99", "0x63", "101"} {
		t.Run(value, func(t *testing.T) {
			f := newSwapEVMFixture(t, false)
			f.funding.AmountRaw = value
			f.adapter.prepareError = errors.New("an inconsistent funding call must not be built")
			result, err := f.send()
			require.ErrorIs(t, err, swap.ErrInvalidQuote)
			require.ErrorIs(t, err, swap.ErrExecutionFailed)
			require.Nil(t, result)
			require.Empty(t, f.adapter.calls)
			require.Zero(t, f.signs)
			require.Zero(t, f.sends)
		})
	}
}

func TestSend_EVMNativeExactInputAcceptsDecimalAndHexValues(t *testing.T) {
	for _, value := range []string{"100", "0x64"} {
		t.Run(value, func(t *testing.T) {
			f := newSwapEVMFixture(t, false)
			f.funding.AmountRaw = value
			result, err := f.send()
			require.NoError(t, err)
			require.Equal(t, "signed-step-1", result.Hash)
			require.Equal(t, "100", f.adapter.calls[0].value.String())
			require.Equal(t, f.funding.To, f.adapter.calls[0].to)
			require.Equal(t, "100", f.transactions[f.steps[0].TransactionID].Amount)
			require.Equal(t, 1, f.signs)
			require.Equal(t, 1, f.sends)
		})
	}
}

func TestSend_EVMResetExactApprovalAndFundingEachWaitForInclusion(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	reset, err := f.send()
	require.NoError(t, err)
	require.True(t, reset.Pending)
	require.Equal(t, "approval_reset", f.steps[0].Purpose)
	require.Equal(t, f.quote.Request.From.Contract, f.transactions[f.steps[0].TransactionID].ToAddress, "approval journal must identify the token contract actually called")
	require.Equal(t, "0", f.transactions[f.steps[0].TransactionID].Amount)
	require.Equal(t, "095ea7b3", hex.EncodeToString(f.adapter.calls[0].data[:4]))
	require.Zero(t, new(big.Int).SetBytes(f.adapter.calls[0].data[36:]).Sign())
	waiting, err := f.send()
	require.NoError(t, err)
	require.True(t, waiting.Pending)
	require.Equal(t, 1, f.signs)
	f.included[f.steps[0].TxHash] = true
	approval, err := f.send()
	require.NoError(t, err)
	require.True(t, approval.Pending)
	require.Equal(t, "approval", f.steps[1].Purpose)
	require.Equal(t, "100", new(big.Int).SetBytes(f.adapter.calls[1].data[36:]).String(), "approve only the accepted input")
	require.Equal(t, f.funding.ApprovalAddress, "0x"+hex.EncodeToString(f.adapter.calls[1].data[16:36]))
	waiting, err = f.send()
	require.NoError(t, err)
	require.True(t, waiting.Pending)
	require.Equal(t, 2, f.signs)
	f.included[f.steps[1].TxHash] = true
	result, err := f.send()
	require.NoError(t, err)
	require.Equal(t, "signed-step-3", result.Hash)
	require.Equal(t, "funding", f.steps[2].Purpose)
	require.Equal(t, f.funding.To, f.adapter.calls[2].to)
	require.Zero(t, f.adapter.calls[2].value.Sign())
	require.Equal(t, "100", f.transactions[f.steps[2].TransactionID].Amount)
	replayed, err := f.send()
	require.NoError(t, err)
	require.Equal(t, result.Hash, replayed.Hash)
	require.Equal(t, 3, f.signs)
	require.Equal(t, 3, f.sends)
	require.Equal(t, 1, f.reservations)
}

func TestSend_EVMConfirmedAllowanceChangeCannotCreateAnotherApproval(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.steps = []models.SwapExecutionStep{{ID: uuid.New(), OrderID: f.order.ID, TransactionID: uuid.New(), Purpose: "approval", TxHash: "already-approved"}}
	f.transactions[f.steps[0].TransactionID] = &models.Transaction{Status: string(types.TxStatusConfirmed)}
	result, err := f.send()
	require.ErrorIs(t, err, swap.ErrExecutionFailed)
	require.ErrorContains(t, err, "confirmed swap allowance changed")
	require.Nil(t, result)
	require.Zero(t, f.signs)
	require.Zero(t, f.sends)
}
