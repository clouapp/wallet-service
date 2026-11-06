package swapcustody_test

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

const lifiDiamond = "0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE"

func call(signature string, args ...[]byte) string {
	data := append([]byte(nil), crypto.Keccak256([]byte(signature))[:4]...)
	for _, arg := range args {
		data = append(data, common.LeftPadBytes(arg, 32)...)
	}
	return "0x" + hex.EncodeToString(data)
}

func word(address string) []byte { return common.HexToAddress(address).Bytes() }

func requireRefused(t *testing.T, f *swapEVMFixture) {
	t.Helper()
	result, err := f.send()
	require.ErrorIs(t, err, swap.ErrExecutionFailed)
	require.ErrorIs(t, err, swap.ErrInvalidQuote)
	require.Nil(t, result)
	require.Empty(t, f.adapter.calls, "a refused call is never built")
	require.Zero(t, f.signs)
	require.Zero(t, f.sends)
}

// The audit probe: the provider names the source token itself as the target with
// a transfer that is not the exact quoted deposit. It must never be signed.
func TestSend_EVMSourceTokenTargetWithAnythingButTheExactDepositIsRefused(t *testing.T) {
	deposit := "0x7777777777777777777777777777777777777777"
	for name, data := range map[string]string{
		"bare transfer selector":     "0xa9059cbb",
		"transfer above the input":   call("transfer(address,uint256)", word(deposit), big.NewInt(101).Bytes()),
		"transfer of the full funds": call("transfer(address,uint256)", word(deposit), big.NewInt(1000).Bytes()),
		"approve to the attacker":    call("approve(address,uint256)", word(deposit), new(big.Int).Lsh(big.NewInt(1), 255).Bytes()),
		"transfer back to itself":    call("transfer(address,uint256)", word("0x2222222222222222222222222222222222222222"), big.NewInt(100).Bytes()),
	} {
		t.Run(name, func(t *testing.T) {
			f := newSwapEVMFixture(t, true)
			f.adapter.allowance = big.NewInt(100)
			f.funding.To = sourceTokenContract
			f.funding.Data = data
			requireRefused(t, f)
		})
	}
}

func TestSend_EVMSourceTokenExactDepositTransferIsFundedWithoutApproval(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.adapter.allowance = big.NewInt(0)
	f.funding.To, f.funding.ApprovalAddress = sourceTokenContract, ""
	f.funding.Data = call("transfer(address,uint256)", word("0x7777777777777777777777777777777777777777"), big.NewInt(100).Bytes())
	result, err := f.send()
	require.NoError(t, err)
	require.Equal(t, "signed-step-1", result.Hash)
	require.Len(t, f.steps, 1)
	require.Equal(t, "funding", f.steps[0].Purpose)
	require.Equal(t, sourceTokenContract, f.adapter.calls[0].to)
	require.Equal(t, 1, f.signs)
}

func TestSend_EVMCallToAnotherCatalogTokenIsRefused(t *testing.T) {
	for _, token := range []bool{false, true} {
		f := newSwapEVMFixture(t, token)
		f.funding.To = otherTokenContract
		f.funding.ApprovalAddress = otherTokenContract
		f.funding.Data = call("transfer(address,uint256)", word("0x7777777777777777777777777777777777777777"), big.NewInt(100).Bytes())
		requireRefused(t, f)
	}
}

func TestSend_EVMBalanceMovingSelectorsAreRefusedOnAnyTarget(t *testing.T) {
	attacker := word("0x7777777777777777777777777777777777777777")
	for name, data := range map[string]string{
		"approve":           call("approve(address,uint256)", attacker, big.NewInt(1).Bytes()),
		"transferFrom":      call("transferFrom(address,address,uint256)", word("0x2222222222222222222222222222222222222222"), attacker, big.NewInt(1).Bytes()),
		"setApprovalForAll": call("setApprovalForAll(address,bool)", attacker, []byte{1}),
		"increaseAllowance": call("increaseAllowance(address,uint256)", attacker, big.NewInt(1).Bytes()),
		"permit2 approve":   call("approve(address,address,uint160,uint48)", word(sourceTokenContract), attacker, big.NewInt(1).Bytes(), big.NewInt(1).Bytes()),
	} {
		t.Run(name, func(t *testing.T) {
			f := newSwapEVMFixture(t, false)
			f.funding.Data = data
			requireRefused(t, f)
		})
	}
}

func TestSend_EVMLiFiCallsOnlyItsDiamondAndApprovesOnlyIt(t *testing.T) {
	t.Run("another target", func(t *testing.T) {
		f := newSwapEVMFixture(t, false)
		f.quote.Provider = swap.ProviderLiFi
		requireRefused(t, f)
	})
	t.Run("token approval to another spender", func(t *testing.T) {
		f := newSwapEVMFixture(t, true)
		f.quote.Provider = swap.ProviderLiFi
		f.funding.To, f.funding.ApprovalAddress = lifiDiamond, routerContract
		requireRefused(t, f)
	})
	t.Run("the Diamond", func(t *testing.T) {
		f := newSwapEVMFixture(t, false)
		f.quote.Provider = swap.ProviderLiFi
		f.funding.To = strings.ToLower(lifiDiamond)
		result, err := f.send()
		require.NoError(t, err)
		require.Equal(t, "signed-step-1", result.Hash)
	})
}

func TestSend_EVMApprovalSpenderMustBeTheCalledContract(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.funding.ApprovalAddress = "0x5555555555555555555555555555555555555555"
	requireRefused(t, f)
}

func routerDeposit(t *testing.T, asset string, quantity int64, memo string) string {
	t.Helper()
	var arguments abi.Arguments
	for _, kind := range []string{"address", "address", "uint256", "string", "uint256"} {
		typ, err := abi.NewType(kind, "", nil)
		require.NoError(t, err)
		arguments = append(arguments, abi.Argument{Type: typ})
	}
	packed, err := arguments.Pack(common.HexToAddress("0x8888888888888888888888888888888888888888"), common.HexToAddress(asset), big.NewInt(quantity), memo, big.NewInt(time.Now().Add(time.Hour).Unix()))
	require.NoError(t, err)
	return "0x" + hex.EncodeToString(append(crypto.Keccak256([]byte("depositWithExpiry(address,address,uint256,string,uint256)"))[:4], packed...))
}

func TestSend_EVMRouterDepositCarriesTheQuotedAssetAmountAndDestination(t *testing.T) {
	destination := "bc1qrvc8mfxeuv0rhwwauvzvwprkkl3uh4ygxln66n"
	native := "0x0000000000000000000000000000000000000000"
	cases := []struct {
		name     string
		asset    string
		quantity int64
		memo     string
		accepted bool
	}{
		{"exact swap memo", native, 100, "=:BTC.BTC:" + destination + ":0/1/0:sk:30", true},
		{"streaming swap with refund", native, 100, "SWAP:BTC.BTC:" + destination + "/0x2222222222222222222222222222222222222222:0/1/0", true},
		{"another destination", native, 100, "=:BTC.BTC:bc1qattacker0000000000000000000000000000000:0", false},
		{"another refund", native, 100, "=:BTC.BTC:" + destination + "/0x9999999999999999999999999999999999999999:0", false},
		{"another amount", native, 99, "=:BTC.BTC:" + destination + ":0", false},
		{"another asset", sourceTokenContract, 100, "=:BTC.BTC:" + destination + ":0", false},
		{"liquidity memo", native, 100, "+:BTC.BTC:" + destination, false},
		{"shorthand asset", native, 100, "=:b:" + destination + ":0", true},
		{"another chain's asset to the same address", native, 100, "=:BSC.BNB:" + destination + ":0", false},
		{"unknown asset notation", native, 100, "=:x:" + destination + ":0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSwapEVMFixture(t, false)
			f.quote.Request.ToAddress = destination
			f.quote.Request.To = swap.Asset{Symbol: "BTC", Chain: "btc", Decimals: 8, Native: true}
			f.funding.Data = routerDeposit(t, tc.asset, tc.quantity, tc.memo)
			if !tc.accepted {
				requireRefused(t, f)
				return
			}
			result, err := f.send()
			require.NoError(t, err)
			require.Equal(t, "signed-step-1", result.Hash)
		})
	}
}

func TestSend_EVMApprovalAndFundingInOneRequestOnceIncluded(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.adapter.allowance = big.NewInt(0)
	f.waitingForInclusion(2 * time.Second)
	reads := 0
	f.adapter.GetTransactionBlockFn = func(context.Context, string) (uint64, error) {
		reads++
		if reads < 3 {
			return 0, nil
		}
		return 1, nil
	}
	result, err := f.send()
	require.NoError(t, err)
	require.Equal(t, "signed-step-2", result.Hash, "the funding is signed in the same request")
	require.Equal(t, []string{"approval", "funding"}, []string{f.steps[0].Purpose, f.steps[1].Purpose})
	require.Equal(t, 2, f.signs)
	require.Equal(t, 2, f.sends)
	require.Equal(t, 1, f.reservations)
}

func TestSend_EVMApprovalNotIncludedBeforeTheDeadlineStaysPending(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.adapter.allowance = big.NewInt(0)
	f.waitingForInclusion(50 * time.Millisecond)
	result, err := f.send()
	require.NoError(t, err)
	require.True(t, result.Pending)
	require.Len(t, f.steps, 1)
	require.Equal(t, 1, f.signs)
}

func TestSend_EVMIncludedApprovalNotVisibleYetIsNeverApprovedTwice(t *testing.T) {
	f := newSwapEVMFixture(t, true)
	f.adapter.allowance = big.NewInt(0)
	f.steps = []models.SwapExecutionStep{{ID: uuid.New(), OrderID: f.order.ID, TransactionID: uuid.New(), Purpose: "approval", TxHash: "included-approval"}}
	f.transactions[f.steps[0].TransactionID] = &models.Transaction{Status: string(types.TxStatusConfirming)}
	f.included["included-approval"] = true
	result, err := f.send()
	require.NoError(t, err)
	require.True(t, result.Pending)
	require.Zero(t, f.signs, "a lagging allowance read is waited for, never approved again")
}

func TestSend_TransferMemoMustPayTheQuoteDestination(t *testing.T) {
	for name, tc := range map[string]struct {
		memo     string
		accepted bool
	}{
		"swap memo to the destination": {"=:ETH.ETH:0x3333333333333333333333333333333333333333:0/1/0:sk:30", true},
		"swap memo to another address": {"=:ETH.ETH:0x9999999999999999999999999999999999999999:0", false},
		"not a swap":                   {"+:ETH.ETH:0x3333333333333333333333333333333333333333", false},
		"swap memo for another asset":  {"=:ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7:0x3333333333333333333333333333333333333333:0", false},
	} {
		t.Run(name, func(t *testing.T) {
			steps := mocks.NewMockSwapSteps(t)
			registry := mocks.NewMockSwapRegistry(t)
			authority := mocks.NewMockSwapAuthority(t)
			wallet := &models.Wallet{ID: uuid.New(), Chain: "btc", DepositAddress: &models.Address{ID: uuid.New(), Address: "bc1qsource"}}
			order := &models.SwapOrder{ID: uuid.New()}
			quote := &swap.Quote{ExpiresAt: time.Now().Add(time.Minute), Request: swap.QuoteRequest{From: swap.Asset{Symbol: "BTC", Chain: "btc", Decimals: 8, Native: true}, To: swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}, FromAddress: "bc1qsource", ToAddress: "0x3333333333333333333333333333333333333333", AmountRaw: "10000"}}
			adapter := &memoChain{MockChain: mocks.NewMockChain("btc")}
			adapter.GetBalanceFn = func(context.Context, string) (*types.Balance, error) {
				return &types.Balance{Amount: big.NewInt(50000)}, nil
			}
			steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{}, nil).Once()
			registry.On("ChainForWallet", wallet).Return(adapter, nil).Once()
			authority.On("ReserveSwapSpend", mock.Anything, wallet, quote, mock.Anything).Return(nil).Once()
			signer := mocks.NewMockPreparedSigner(t)
			if tc.accepted {
				signer.On("SignPrepared", mock.Anything, wallet.ID, "test-only-passphrase", adapter, mock.Anything).Return(nil, context.Canceled).Once()
			}
			service := swapcustody.New(swapcustody.Deps{Authority: authority, Registry: registry, Steps: steps, Signer: signer, Cache: memcache.New()})
			_, err := service.Send(context.Background(), order, wallet, quote, &swap.Funding{Kind: "transfer", To: "bc1qvault", AmountRaw: "10000", Memo: tc.memo}, swap.ExecuteInput{Passphrase: "test-only-passphrase"})
			require.ErrorIs(t, err, swap.ErrExecutionFailed)
			if tc.accepted {
				require.Equal(t, 1, adapter.built)
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.ErrorIs(t, err, swap.ErrInvalidQuote)
			require.Zero(t, adapter.built)
		})
	}
}

type memoChain struct {
	*mocks.MockChain
	built int
}

func (c *memoChain) BuildMemoTransfer(context.Context, types.TransferRequest, string) (*types.UnsignedTx, error) {
	c.built++
	return &types.UnsignedTx{ChainID: "btc"}, nil
}

type settlerFunc func(context.Context, *models.Transaction) (*models.Transaction, error)

func (f settlerFunc) SettleUnanswered(ctx context.Context, tx *models.Transaction) (*models.Transaction, error) {
	return f(ctx, tx)
}

func TestSettleFunding_ResendsUnansweredBytesAndReadsTheOutcome(t *testing.T) {
	pendingPayload := `{"broadcast_outcome":"pending","signed_tx":"enc:v1:fixture"}`
	cases := []struct {
		name     string
		tx       models.Transaction
		receipt  func(context.Context, string) (uint64, error)
		resends  int
		expected swap.FundingState
	}{
		{"unanswered send is sent again and found", models.Transaction{Status: "confirming", RawPayload: pendingPayload}, func(context.Context, string) (uint64, error) { return 7, nil }, 1, swap.FundingIncluded},
		{"answered send is only read", models.Transaction{Status: "confirming", RawPayload: `{"broadcast_outcome":"accepted"}`}, func(context.Context, string) (uint64, error) { return 0, nil }, 0, swap.FundingUnknown},
		{"reverted on chain", models.Transaction{Status: "confirming", RawPayload: `{}`}, func(context.Context, string) (uint64, error) { return 0, chain.ErrTransactionFailed }, 0, swap.FundingFailed},
		{"failed by the tracker", models.Transaction{Status: "failed"}, nil, 0, swap.FundingFailed},
		{"confirmed by the tracker", models.Transaction{Status: "confirmed"}, nil, 0, swap.FundingIncluded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wallet := &models.Wallet{ID: uuid.New(), Chain: "eth"}
			order := &models.SwapOrder{ID: uuid.New()}
			tx := tc.tx
			tx.ID, tx.TxHash = uuid.New(), "funding-hash"
			steps := mocks.NewMockSwapSteps(t)
			steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{{Purpose: "approval", TransactionID: uuid.New()}, {Purpose: "funding", TransactionID: tx.ID, TxHash: tx.TxHash}}, nil).Once()
			transactions := mocks.NewMockSwapTransactions(t)
			transactions.On("FindByID", mock.Anything, tx.ID).Return(&tx, nil).Once()
			registry := mocks.NewMockSwapRegistry(t)
			adapter := mocks.NewMockChain("eth")
			if tc.receipt != nil {
				adapter.GetTransactionBlockFn = tc.receipt
				registry.On("ChainForWallet", wallet).Return(adapter, nil).Once()
			}
			resends := 0
			settler := settlerFunc(func(_ context.Context, row *models.Transaction) (*models.Transaction, error) {
				resends++
				require.Equal(t, tx.ID, row.ID)
				return row, nil
			})
			service := swapcustody.New(swapcustody.Deps{Steps: steps, Transactions: transactions, Registry: registry, Settler: settler})
			state, err := service.SettleFunding(context.Background(), order, wallet)
			require.NoError(t, err)
			require.Equal(t, tc.expected, state)
			require.Equal(t, tc.resends, resends)
		})
	}
}

type solanaSwapChain struct {
	*mocks.MockChain
	err error
}

func (c *solanaSwapChain) PrepareSolanaSwap(context.Context, string, string, *big.Int, *types.Token) (*types.UnsignedTx, error) {
	return nil, c.err
}

func TestSend_SolanaProgramOutsideTheAllowlistIsAnInvalidQuote(t *testing.T) {
	steps := mocks.NewMockSwapSteps(t)
	registry := mocks.NewMockSwapRegistry(t)
	authority := mocks.NewMockSwapAuthority(t)
	wallet := &models.Wallet{ID: uuid.New(), Chain: "sol", DepositAddress: &models.Address{ID: uuid.New(), Address: "wallet"}}
	order := &models.SwapOrder{ID: uuid.New()}
	quote := &swap.Quote{ExpiresAt: time.Now().Add(time.Minute), Request: swap.QuoteRequest{From: swap.Asset{Symbol: "SOL", Chain: "sol", Decimals: 9, Native: true}, FromAddress: "wallet", AmountRaw: "1000"}}
	adapter := &solanaSwapChain{MockChain: mocks.NewMockChain("sol"), err: errors.Join(chain.ErrSwapTransactionRefused, errors.New("program Stake11111111111111111111111111111111111111"))}
	adapter.GetBalanceFn = func(context.Context, string) (*types.Balance, error) {
		return &types.Balance{Amount: big.NewInt(5000)}, nil
	}
	steps.On("ExecutionSteps", mock.Anything, order.ID).Return([]models.SwapExecutionStep{}, nil).Once()
	registry.On("ChainForWallet", wallet).Return(adapter, nil).Once()
	authority.On("ReserveSwapSpend", mock.Anything, wallet, quote, mock.Anything).Return(nil).Once()
	service := swapcustody.New(swapcustody.Deps{Authority: authority, Registry: registry, Steps: steps, Signer: mocks.NewMockPreparedSigner(t), Cache: memcache.New()})
	_, err := service.Send(context.Background(), order, wallet, quote, &swap.Funding{Kind: "solana", Data: "AQID"}, swap.ExecuteInput{Passphrase: "test-only-passphrase"})
	require.ErrorIs(t, err, swap.ErrInvalidQuote)
	require.ErrorIs(t, err, swap.ErrExecutionFailed)
}

func TestSend_EVMStandingAllowanceOfAnotherTokenIsRevokedBeforeFunding(t *testing.T) {
	f := newSwapEVMFixture(t, false)
	f.adapter.standing[strings.ToLower(otherTokenContract)] = big.NewInt(1000)
	f.waitingForInclusion(2 * time.Second)
	f.adapter.GetTransactionBlockFn = func(context.Context, string) (uint64, error) { return 1, nil }
	result, err := f.send()
	require.NoError(t, err)
	require.Equal(t, "signed-step-2", result.Hash)
	require.Equal(t, []string{"approval_revoke", "funding"}, []string{f.steps[0].Purpose, f.steps[1].Purpose})
	require.Equal(t, otherTokenContract, f.adapter.calls[0].to, "the leftover allowance is revoked on its token")
	require.Equal(t, "095ea7b3", hex.EncodeToString(f.adapter.calls[0].data[:4]))
	require.Zero(t, new(big.Int).SetBytes(f.adapter.calls[0].data[36:]).Sign())
	require.Equal(t, routerContract, "0x"+hex.EncodeToString(f.adapter.calls[0].data[16:36]))
	require.Zero(t, f.adapter.standing[strings.ToLower(otherTokenContract)].Sign())
}

func TestSend_EVMRevokedButNotVisibleIsNeverRevokedTwice(t *testing.T) {
	f := newSwapEVMFixture(t, false)
	f.adapter.standing[strings.ToLower(otherTokenContract)] = big.NewInt(1000)
	f.steps = []models.SwapExecutionStep{{ID: uuid.New(), OrderID: f.order.ID, TransactionID: uuid.New(), Purpose: "approval_revoke", TxHash: "included-revoke"}}
	f.transactions[f.steps[0].TransactionID] = &models.Transaction{Status: string(types.TxStatusConfirming), ToAddress: otherTokenContract}
	f.included["included-revoke"] = true
	result, err := f.send()
	require.NoError(t, err)
	require.True(t, result.Pending)
	require.Zero(t, f.signs)
}

func TestSend_EVMCallToATokenTheWalletHoldsIsRefused(t *testing.T) {
	f := newSwapEVMFixture(t, false)
	unlisted := "0x9999999999999999999999999999999999999999"
	f.funding.To = unlisted
	f.adapter.GetTokenBalanceFn = func(_ context.Context, _ string, token types.Token) (*types.Balance, error) {
		if strings.EqualFold(token.Contract, unlisted) {
			return &types.Balance{Amount: big.NewInt(5)}, nil
		}
		return &types.Balance{Amount: new(big.Int)}, nil
	}
	requireRefused(t, f)
}

func TestSend_EVMMoreBalanceMovingFunctionsAreRefused(t *testing.T) {
	attacker := word("0x7777777777777777777777777777777777777777")
	for name, data := range map[string]string{
		"transferAndCall":  call("transferAndCall(address,uint256,bytes)", attacker, big.NewInt(1).Bytes(), big.NewInt(96).Bytes(), []byte{}),
		"increaseApproval": call("increaseApproval(address,uint256)", attacker, big.NewInt(1).Bytes()),
		"approveAndCall":   call("approveAndCall(address,uint256,bytes)", attacker, big.NewInt(1).Bytes(), big.NewInt(96).Bytes(), []byte{}),
		"erc777 send":      call("send(address,uint256,bytes)", attacker, big.NewInt(1).Bytes(), big.NewInt(96).Bytes(), []byte{}),
		"dai push":         call("push(address,uint256)", attacker, big.NewInt(1).Bytes()),
	} {
		t.Run(name, func(t *testing.T) {
			f := newSwapEVMFixture(t, true)
			f.adapter.allowance = big.NewInt(100)
			f.funding.Data = data
			requireRefused(t, f)
		})
	}
}

type recordingTransactions struct {
	*mocks.MockSwapTransactions
	recorded map[uuid.UUID]string
}

func (r *recordingTransactions) RecordBroadcast(_ context.Context, id uuid.UUID, _ string, payload string) error {
	r.recorded[id] = payload
	return nil
}

func TestSend_AnAcceptedSendDropsItsSignedBytes(t *testing.T) {
	f := newSwapEVMFixture(t, false)
	recorder := &recordingTransactions{MockSwapTransactions: mocks.NewMockSwapTransactions(t), recorded: map[uuid.UUID]string{}}
	f.deps.Transactions = recorder
	f.service = swapcustody.New(f.deps)
	result, err := f.send()
	require.NoError(t, err)
	require.Equal(t, "signed-step-1", result.Hash)
	payload := recorder.recorded[f.steps[0].TransactionID]
	require.JSONEq(t, `{"broadcast_outcome":"accepted"}`, payload, "the sealed bytes are dropped once a node accepted them")
}
