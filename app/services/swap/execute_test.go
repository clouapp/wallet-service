package swap_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type executionFixture struct {
	store       *mocks.MockSwapStore
	execution   *mocks.MockSwapExecution
	provider    *mocks.MockSwapProvider
	credentials *mocks.MockSwapCredentials
	verifier    *mocks.MockSwapVerifier
	service     *swap.Service
	account     *models.Account
	source      *models.Wallet
	destination *models.Wallet
	quote       *swap.Quote
	row         *models.SwapQuote
	in          swap.ExecuteInput
}

func executionSetup(t *testing.T) *executionFixture {
	t.Helper()
	f := &executionFixture{store: mocks.NewMockSwapStore(t), execution: mocks.NewMockSwapExecution(t), provider: mocks.NewMockSwapProvider(t), credentials: mocks.NewMockSwapCredentials(t), verifier: mocks.NewMockSwapVerifier(t)}
	f.account = &models.Account{ID: uuid.New(), Status: "active", Environment: "prod"}
	f.source = &models.Wallet{ID: uuid.New(), AccountID: &f.account.ID, Status: "active", Chain: "eth", DepositAddress: &models.Address{ID: uuid.New(), Address: "0x2222222222222222222222222222222222222222"}}
	f.destination = &models.Wallet{ID: uuid.New(), AccountID: &f.account.ID, Status: "active", Chain: "bsc", DepositAddress: &models.Address{ID: uuid.New(), Address: "0x3333333333333333333333333333333333333333"}}
	f.quote = &swap.Quote{ID: uuid.New(), Provider: swap.ProviderLiFi, RouteID: "pinned-route", Request: routingRequest(), ExpectedRaw: "1000000000000000000", MinimumRaw: "990000000000000000", ExpiresAt: time.Now().Add(time.Minute), Payload: json.RawMessage(`{"id":"pinned-route"}`)}
	f.quote.Request.To = swap.Asset{Symbol: "BNB", Chain: "bsc", NetworkID: 56, Decimals: 18, Native: true}
	request, err := json.Marshal(f.quote.Request)
	require.NoError(t, err)
	f.row = &models.SwapQuote{ID: f.quote.ID, AccountID: f.account.ID, SourceWalletID: f.source.ID, DestinationWalletID: f.destination.ID, Provider: f.quote.Provider, RouteID: f.quote.RouteID, RequestPayload: string(request), ExpectedRaw: f.quote.ExpectedRaw, MinimumRaw: f.quote.MinimumRaw, RoutePayload: string(f.quote.Payload), FeesPayload: "[]", AttemptsPayload: "[]", ExpiresAt: f.quote.ExpiresAt}
	f.in = swap.ExecuteInput{QuoteID: f.quote.ID, IdempotencyKey: uuid.New(), UserID: uuid.New(), Passphrase: "test-only-passphrase", TOTP: "123456"}
	policy := mocks.NewMockSwapPolicy(t)
	wallets := mocks.NewMockSwapWallets(t)
	catalog := mocks.NewMockSwapCatalog(t)
	f.provider.On("Name").Return(swap.ProviderLiFi)
	factory, err := swap.NewFactory(f.provider)
	require.NoError(t, err)
	// Optional expectations keep replay tests honest: none is needed unless a
	// first execution or a verified continuation actually advances the order.
	policy.On("Resolve", mock.Anything, "ETH", "eth").Return(routingConfig(), nil).Maybe()
	wallets.On("FindByID", mock.Anything, f.destination.ID).Return(f.destination, nil).Maybe()
	ethID, bscID := int64(1), int64(56)
	catalog.On("FindByID", mock.Anything, "eth").Return(&models.Chain{ID: "eth", Status: "active", NativeSymbol: "ETH", NativeDecimals: 18, NetworkID: &ethID}, nil).Maybe()
	catalog.On("FindByID", mock.Anything, "bsc").Return(&models.Chain{ID: "bsc", Status: "active", NativeSymbol: "BNB", NativeDecimals: 18, NetworkID: &bscID}, nil).Maybe()
	catalog.On("ValidateAddress", mock.Anything, mock.Anything).Return(true).Maybe()
	f.service = swap.NewService(swap.Deps{Factory: factory, Store: f.store, Wallets: wallets, Catalog: catalog, Policy: policy, Credentials: f.credentials, Execution: f.execution, Verifier: f.verifier})
	return f
}

func (f *executionFixture) firstExecution() {
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(nil, swap.ErrNotFound).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.execution.On("Authorize", mock.Anything, f.source, mock.Anything).Return(nil).Once()
	f.store.On("CreateOrder", mock.Anything, mock.Anything).Return(nil).Once()
	f.credentials.On("Provider", mock.Anything, swap.ProviderLiFi).Return(swap.ProviderConfig{Enabled: true}, nil).Once()
	f.provider.On("Build", mock.Anything, mock.MatchedBy(func(q *swap.Quote) bool { return q.ID == f.quote.ID && q.RouteID == "pinned-route" }), mock.Anything).Return(&swap.Funding{Kind: "transfer", To: "deposit", AmountRaw: f.quote.Request.AmountRaw}, nil).Once()
	f.store.On("SaveFunding", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
}

func TestExecute_ReplaysWithoutSigningOrContactingAProvider(t *testing.T) {
	f := executionSetup(t)
	existing := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusReconciling, SourceTxHash: "persisted-before-send"}
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(existing, nil).Once()
	order, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.NoError(t, err)
	require.Same(t, existing, order)
	f.execution.AssertNotCalled(t, "Authorize", mock.Anything, mock.Anything, mock.Anything)
	f.provider.AssertNotCalled(t, "Build", mock.Anything, mock.Anything, mock.Anything)
}

func TestExecute_UnknownSendRemainsReconcilingAndDoesNotFallback(t *testing.T) {
	f := executionSetup(t)
	f.firstExecution()
	f.execution.On("Send", mock.Anything, mock.Anything, f.source, mock.Anything, mock.Anything, mock.Anything).Return(&swap.SendResult{Hash: "durable-hash", Uncertain: true}, errors.New("connection reset after send")).Once()
	f.store.On("RecordSource", mock.Anything, mock.Anything, "durable-hash", swap.StatusReconciling).Return(nil).Once()
	order, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.NoError(t, err)
	require.Equal(t, swap.StatusReconciling, order.Status)
	require.Equal(t, "durable-hash", order.SourceTxHash)
	f.provider.AssertNotCalled(t, "Quote", mock.Anything, mock.Anything, mock.Anything)
}

func TestExecute_ApprovalPendingIsNotAFailedOrCompletedSwap(t *testing.T) {
	f := executionSetup(t)
	f.firstExecution()
	f.execution.On("Send", mock.Anything, mock.Anything, f.source, mock.Anything, mock.Anything, mock.Anything).Return(&swap.SendResult{Pending: true}, nil).Once()
	order, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.NoError(t, err)
	require.Equal(t, swap.StatusPreparing, order.Status)
	require.Empty(t, order.SourceTxHash)
}

func TestExecute_RejectsContinuationByAnotherActor(t *testing.T) {
	f := executionSetup(t)
	other := uuid.New()
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(&models.SwapOrder{QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusPreparing, AuthorizedUserID: &other, AuthorizedUntil: time.Now().Add(time.Minute)}, nil).Once()
	_, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.ErrorIs(t, err, swap.ErrConflict)
}

func TestRefresh_ExpiresAnUnfundedOrderWithoutContactingTheProvider(t *testing.T) {
	f := executionSetup(t)
	f.row.ExpiresAt = time.Now().Add(-time.Minute)
	order := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusPreparing}
	f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(order, nil).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.store.On("RecordProgress", mock.Anything, order, swap.StatusPreparing).Return(nil).Once()
	result, err := f.service.Refresh(context.Background(), f.account, f.source, order.ID)
	require.NoError(t, err)
	require.Equal(t, swap.StatusExpired, result.Status)
	require.Empty(t, result.SourceTxHash)
	f.provider.AssertNotCalled(t, "Status", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRefresh_RequiresIndependentCreditAndUsesItsAmount(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider alone cannot credit", true: "credits actual finalized amount"}[confirmed], func(t *testing.T) {
			f := executionSetup(t)
			order := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, DestinationWalletID: f.destination.ID, Provider: swap.ProviderLiFi, Status: swap.StatusProcessing, SourceTxHash: "send"}
			f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(order, nil).Once()
			f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
			f.credentials.On("Provider", mock.Anything, swap.ProviderLiFi).Return(swap.ProviderConfig{}, nil).Once()
			f.provider.On("Status", mock.Anything, mock.Anything, "send", mock.Anything).Return(&swap.Progress{Status: swap.StatusCompleted, ReceivingHash: "receive", ReceivedRaw: "9999999999999999999999999", ReceivingAsset: f.quote.Request.To}, nil).Once()
			f.verifier.On("Credit", mock.Anything, mock.MatchedBy(func(request swap.CreditRequest) bool {
				return request.OrderID == order.ID && request.WalletID == f.destination.ID && request.Asset.Same(f.quote.Request.To) && request.Hash == "receive" && request.Direction == "receive"
			})).Return("995000000000000000", confirmed, nil).Once()
			f.store.On("RecordProgress", mock.Anything, order, swap.StatusProcessing).Return(nil).Once()
			result, err := f.service.Refresh(context.Background(), f.account, f.source, order.ID)
			require.NoError(t, err)
			if confirmed {
				require.Equal(t, swap.StatusCompleted, result.Status)
				require.Equal(t, "995000000000000000", result.ReceivedRaw)
			} else {
				require.Equal(t, swap.StatusProcessing, result.Status)
				require.Empty(t, result.DestinationTxHash)
			}
		})
	}
}
