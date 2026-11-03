package ramp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

const eventRowID = uint(9)

// standingAdapter is a provider that serves the rail both ways: the fat
// ProviderAdapter the factory registers, and the standing-account port the
// account branch type-asserts off it. Mockery generates the two separately
// because they are two interfaces; a real provider implements both on one type,
// and so does this. Code() is stated explicitly because both embeds carry one.
type standingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockVirtualAccountAdapter
}

func (a *standingAdapter) Code() ramp.ProviderCode { return a.MockProviderAdapter.Code() }

// deliveringProviderAdapter also carries the optional on-ramp delivery port, so
// the reconciler's capability probe — which reads it off the factory — finds a
// provider that needs a second hop. Its DeliverOnramp is never reached: the
// sweep calls the service through OnrampDeliverer, and this type only has to
// make the provider *look* like one that delivers in two hops, which is exactly
// what the probe is asking.
type deliveringProviderAdapter struct {
	*standingAdapter
}

func (a *deliveringProviderAdapter) DeliverOnramp(
	context.Context, ramp.CustomerRef, ramp.OnrampDeliveryRequest,
) (*ramp.RampOrder, error) {
	return nil, nil
}

// recordingDeliverer stands in for the on-ramp service's second hop. The
// processor only knows the interface, which is the point: the delivery is a
// client-service concern and the processor's job is to say when it is due.
type recordingDeliverer struct {
	delivered []*models.Transaction
	err       error
}

func (d *recordingDeliverer) Deliver(_ context.Context, tx *models.Transaction) error {
	d.delivered = append(d.delivered, tx)
	return d.err
}

type harness struct {
	processor   *rampsvc.WebhookProcessor
	deliverer   *recordingDeliverer
	adapter     *mocks.MockProviderAdapter
	standing    *mocks.MockVirtualAccountAdapter
	events      *mocks.MockWebhookEventRepository
	integRepo   *mocks.MockCustomerProviderIntegrationRepository
	accounts    *mocks.MockCustomerVirtualAccountRepository
	addresses   *mocks.MockUserLiquidationAddressRepository
	broadcaster *mocks.MockBroadcaster
	notifier    *mocks.MockNotifier
	members     *mocks.MockCustomerMemberRepository
	features    contracts.Feature
	// announced and verdicts are the order statuses and KYC verdicts the
	// devices were told about, in order. The notifier decides for itself which
	// of them become a notification; what the processor owes it is the change.
	announced []string
	verdicts  []string
	// db is the transaction WithLocked hands the processor. Every write an
	// apply makes goes through it, so an expectation on db is an expectation
	// that the write happened inside the locked transaction.
	db *mocks.MockWebhookTx
}

// openFeatures is the flag catalogue on an in-memory store, where every rail
// ships open. Most of this file is about decoding, ordering and persistence
// rather than about the flag.
func openFeatures() contracts.Feature {
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	return m
}

// closedPayin is the payin rail switched off for everyone.
func closedPayin(t *testing.T) contracts.Feature {
	t.Helper()
	m := openFeatures()
	require.NoError(t, m.Deactivate(context.Background(), ramp.RailPixPayin.Flag(), nil))
	return m
}

func newHarness(t *testing.T) *harness { return newHarnessWithFeatures(t, openFeatures()) }

func newHarnessWithFeatures(t *testing.T, features contracts.Feature) *harness {
	t.Helper()
	factory := mock.Factory()
	factory.Log()

	h := &harness{
		adapter:     mocks.NewMockProviderAdapter(t),
		standing:    mocks.NewMockVirtualAccountAdapter(t),
		events:      mocks.NewMockWebhookEventRepository(t),
		integRepo:   mocks.NewMockCustomerProviderIntegrationRepository(t),
		accounts:    mocks.NewMockCustomerVirtualAccountRepository(t),
		addresses:   mocks.NewMockUserLiquidationAddressRepository(t),
		broadcaster: mocks.NewMockBroadcaster(t),
		notifier:    mocks.NewMockNotifier(t),
		db:          mocks.NewMockWebhookTx(t),
	}
	h.notifier.EXPECT().TransactionUpdated(testifymock.Anything).Run(func(tx *models.Transaction) {
		h.announced = append(h.announced, tx.Status)
	}).Maybe()
	h.notifier.EXPECT().KycUpdated(testifymock.Anything, testifymock.Anything).Run(func(_ string, row *models.CustomerProviderKyc) {
		h.verdicts = append(h.verdicts, row.KycStatus)
	}).Maybe()
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.adapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	h.features = features
	h.members = support.IndividualMembers(t)
	h.deliverer = &recordingDeliverer{}
	h.build()
	return h
}

// build makes the processor over the harness's collaborators.
func (h *harness) build() {
	provider := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(provider), Events: h.events, Integrations: h.integRepo, Accounts: h.accounts,
		Members: h.members, Broadcaster: h.broadcaster, Notifier: h.notifier, Features: h.features,
		Deliverer: h.deliverer, Addresses: h.addresses,
	})
}

// withMembers rebuilds the processor with customerID acted for by userIDs.
func (h *harness) withMembers(t *testing.T, customerID uint, userIDs ...uint) {
	t.Helper()
	h.members = mocks.NewMockCustomerMemberRepository(t)
	h.members.EXPECT().MembersOf(customerID).Return(userIDs, nil).Once()
	h.build()
}

// storedEvent is the row Process loads and then re-reads under the lock before
// it applies anything. The locked run is optional because a decode that fails
// never opens the transaction.
func (h *harness) storedEvent(row *models.WebhookEvent) {
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.locked(row).Maybe()
}

// locked answers WithLocked for row: it runs the processor's callback with row
// as the locked read and db as the transaction.
func (h *harness) locked(row *models.WebhookEvent) *mocks.MockWebhookEventRepository_WithLocked_Call {
	return h.events.EXPECT().WithLocked(row.ID, testifymock.Anything).
		RunAndReturn(func(_ uint, fn func(*models.WebhookEvent, repositories.WebhookTx) error) error {
			return fn(row, h.db)
		})
}

// lockedOnce answers one WithLocked call with row as the locked read. The
// failure stamp re-reads the event under its own lock, so a test that needs
// that read to differ from the apply's queues two of these.
func (h *harness) lockedOnce(row *models.WebhookEvent) {
	h.events.EXPECT().WithLocked(row.ID, testifymock.Anything).
		RunAndReturn(func(_ uint, fn func(*models.WebhookEvent, repositories.WebhookTx) error) error {
			return fn(row, h.db)
		}).Once()
}

func eventRow() *models.WebhookEvent {
	return &models.WebhookEvent{
		Model: orm.Model{ID: eventRowID}, Provider: models.ProviderBridge,
		EventID: "evt_1", RawPayload: `{"event_id":"evt_1"}`, ReceivedAt: time.Now(),
	}
}

func rampOrder(status string) *models.Transaction {
	return &models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_1", GatewayOrderID: "trf_1",
		Status: status, AmountFiat: "100.00", AmountToken: "18.22", TargetToken: "usdc",
	}
}

func ozavOnrampOrder(status string) *models.Transaction {
	tx := rampOrder(status)
	tx.Provider = models.ProviderOzav
	return tx
}

// virtualAccountRow is the standing account a deposit event resolves to. It is
// what carries the user, the destination and — the field no activity can name —
// the fiat currency the deposit arrived in.
func virtualAccountRow() *models.CustomerVirtualAccount {
	return &models.CustomerVirtualAccount{
		Model: orm.Model{ID: 3}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		ExternalAccountID: "va_1", SourceCurrency: "brl",
		Status:              models.VirtualAccountStatusActive,
		DestinationCurrency: "usdc", DestinationChain: "solana",
		DestinationAddress:  "So1Wa11et",
		FeePercent:          support.Ptr("1.50"),
		DepositInstructions: `{"br_code":"0002010102"}`,
		RawPayload:          `{"id":"va_1"}`,
	}
}

// depositEvent is one activity of a deposit, already normalized by the adapter.
// Nothing on it is top-level: a virtual-account deposit sets no Status, TxHash
// or ProviderOrderID, so a processor reaching for those reads a zero value.
func depositEvent(deposit *ramp.VirtualAccountDeposit) *ramp.WebhookResult {
	return &ramp.WebhookResult{
		Kind:      ramp.WebhookKindVirtualAccountDeposit,
		EventID:   "evt_1",
		EventType: "virtual_account.activity.funds_received",
		// Both ids are the same value read out of the activity, which is how the
		// adapter fills them.
		ProviderAccountID: deposit.ProviderAccountID,
		Deposit:           deposit,
	}
}

type traceKey struct{}

// The caller's context reaches every provider-facing call the processor makes,
// so the worker's deadline and request-scoped values govern them.
func TestProcessCarriesTheCallersContextToTheProviderCalls(t *testing.T) {
	var asked context.Context
	features := mocks.NewMockFeature(t)
	features.EXPECT().IsActive(testifymock.Anything, ramp.RailPixPayin.Flag(), models.UserFlagScope(7)).
		RunAndReturn(func(ctx context.Context, _ string, _ any) (bool, error) {
			asked = ctx
			return true, nil
		}).Once()
	h := newHarnessWithFeatures(t, features)
	ctx := context.WithValue(context.Background(), traceKey{}, "job-1")
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(ctx, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(virtualAccountRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(nil, nil).Once()
	h.db.EXPECT().CreateTransaction(testifymock.Anything).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(ctx, eventRowID))
	require.Same(t, ctx, asked, "the deposit's rail check runs under the caller's context")
}

// The first event is the first XIP hears of the money: there is no client
// create on this rail, so funds_received is what brings the row into existence.
func TestProcessCreatesTheTransactionFromTheDepositEvent(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()
	occurred := time.Now()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
			FeeAmount: "1.50", SenderName: "Maria Silva", SenderReference: "E1234",
			OccurredAt: &occurred,
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(nil, nil).Once()

	var created *models.Transaction
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(
		func(tx *models.Transaction) error {
			tx.ID = 42
			created = tx
			return nil
		}).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusFundsReceived, created.Status)
	require.Equal(t, uint(7), created.UserID, "the account row is what names the user")
	require.Equal(t, models.TransactionTypeOnramp, created.Type)
	require.Equal(t, models.RailPixPayin, *created.Rail)
	require.Equal(t, "dep_1", created.GatewayOrderID)
	require.Equal(t, "dep_1", *created.DepositID)
	require.Equal(t, uint(3), *created.VirtualAccountID)
	require.NotEmpty(t, created.ExternalID)
	require.Equal(t, "100.0", created.AmountFiat)
	require.Equal(t, "0", created.AmountToken, "no receipt yet means nothing was delivered, not an empty column")
	require.Equal(t, "1.50", *created.FeeAmount)
	require.Equal(t, "brl", *created.FeeCurrency, "the fiat currency comes from the account row, never from the event")
	require.Equal(t, "Maria Silva", *created.SenderName)
	require.Equal(t, "E1234", *created.SenderReference)
	require.Nil(t, created.ExpiresAt, "a deposit arrives already funded and has no payment window")
	require.Nil(t, created.IdempotencyKey, "there is no client create to be idempotent about")
	require.Equal(t, uint(42), *row.TransactionID)
}

// A deposit into a company's account opens the company's order, its actor the
// member who provisioned the account (spec §18.6): transactions.user_id stays
// who started the money's path.
func TestProcessOpensACompanysDepositForTheCompany(t *testing.T) {
	const company = uint(1_000_000_000_007)
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()
	account.CustomerID = company
	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_company", ProviderAccountID: "va_1", Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_company").Return(nil, nil).Once()
	var created *models.Transaction
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(func(tx *models.Transaction) error {
		created = tx
		return nil
	}).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, company, created.CustomerID)
	require.Equal(t, account.UserID, created.UserID, "the member who provisioned the account")
}

// Duplicate delivery is normal. The deposit id is the identity, so the second
// copy moves the row it already created instead of creating another.
func TestProcessDoesNotCreateASecondTransactionForTheSameDeposit(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()
	existing := &models.Transaction{
		Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_1", GatewayOrderID: "dep_1",
		DepositID: support.Ptr("dep_1"), Status: models.TxStatusFundsReceived,
		AmountFiat: "100.0", AmountToken: "0", TargetToken: "USDC",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusProcessing, SourceAmount: "100.0", DestinationAmount: "97.5",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(existing, nil).Once()
	h.db.EXPECT().UpdateTransaction(existing).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusProcessing, existing.Status)
	require.Equal(t, "97.5", existing.AmountToken)
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}

// Out-of-order delivery is the normal case, not an edge one: the payment event
// carries the whole receipt, so it can create the row already completed.
func TestProcessCreatesACompletedRowWhenThePaymentEventArrivesFirst(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()
	occurred := time.Now().Add(-time.Minute)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_2", ProviderAccountID: "va_1",
			Status: models.TxStatusCompleted,
			// Both legs come off the receipt. The event's own `amount` is the
			// crypto one here, and reading it would put 97.5 USDC in the reais
			// column — which is why the decoder resolved these first.
			SourceAmount: "100.0", DestinationAmount: "97.5",
			FeeAmount: "1.50", ExchangeFeeAmount: "0.25",
			TxHash: "0xfeed", OccurredAt: &occurred,
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_2").Return(nil, nil).Once()

	var created *models.Transaction
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(
		func(tx *models.Transaction) error {
			tx.ID = 43
			created = tx
			return nil
		}).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, created.Status)
	require.Equal(t, "100.0", created.AmountFiat)
	require.Equal(t, "97.5", created.AmountToken)
	require.Equal(t, "0xfeed", *created.DepositTxSignature)
	require.Equal(t, "1.50", *created.FeeAmount)
	require.Equal(t, "0.25", *created.ExchangeFeeAmount)
	require.Equal(t, "brl", *created.FeeCurrency)
	require.NotNil(t, created.CompletedAt)
	require.Equal(t, occurred.Unix(), created.CompletedAt.Unix(), "completion is stamped from the event's own time")
	require.Equal(t, []string{models.TxStatusCompleted}, h.announced, "the devices hear the deposit settled")
}

// The other half of out-of-order: the funds event that lost the race must not
// walk the settled row back to funds_received.
func TestProcessRefusesALateFundsEventOnACompletedDeposit(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()
	settled := &models.Transaction{
		Model: orm.Model{ID: 43}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, GatewayOrderID: "dep_2",
		DepositID: support.Ptr("dep_2"), Status: models.TxStatusCompleted,
		AmountFiat: "100.0", AmountToken: "97.5",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_2", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_2").Return(settled, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, settled.Status)
	require.Equal(t, "97.5", settled.AmountToken)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// Bridge documents microdeposit funds as never onramped, so the adapter files
// them as unknown. A transaction for one would show the user money that does
// not exist.
func TestProcessCreatesNothingForAMicrodeposit(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindUnknown, EventID: "evt_1",
		EventType: "virtual_account.activity.microdeposit", ProviderAccountID: "va_1",
	}, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Nil(t, row.ProcessingError, "authentic and acknowledged, just without effect")
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
	h.accounts.AssertNotCalled(t, "FindByProviderAndExternalID", testifymock.Anything, testifymock.Anything)
}

// Money arriving in an account we do not know is an operational anomaly. It
// belongs in processing_error, where an operator finds it, not in silence.
func TestProcessErrorsOnADepositIntoAnUnknownVirtualAccount(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_3", ProviderAccountID: "va_ghost", Status: models.TxStatusFundsReceived,
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_ghost").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.Contains(t, *row.ProcessingError, "va_ghost")
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}

// An account activity is a change description, not a snapshot: every empty
// field means "this event says nothing about it". Applying it as a snapshot
// would blank the destination and the status the row already holds.
func TestProcessRereadsTheAccountInsteadOfTrustingTheEvent(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindVirtualAccount, EventID: "evt_1",
		EventType: "virtual_account.activity.account_update", ProviderAccountID: "va_1",
		ExternalCustomerID: "cus_1",
		// account_update carries no status on the wire, and asserting `active`
		// here would revive an account somebody deactivated on purpose.
		Account: &ramp.VirtualAccount{ProviderAccountID: "va_1"},
	}, nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").Return(
		&ramp.VirtualAccount{
			ProviderAccountID: "va_1", Status: models.VirtualAccountStatusActive,
			Destination: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Address: "NewWa11et"},
			FeePercent:  "2.00",
			Raw:         json.RawMessage(`{"id":"va_1","developer_fee_percent":"2.00"}`),
		}, nil).Once()
	h.db.EXPECT().FindVirtualAccountForUpdate(account.ID).Return(account, nil).Once()
	h.db.EXPECT().UpdateVirtualAccount(account).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, "NewWa11et", account.DestinationAddress, "the provider's read is what says where the money goes")
	require.Equal(t, "2.00", *account.FeePercent)
	require.Equal(t, models.VirtualAccountStatusActive, account.Status, "an empty field on the event never clears one")
	require.Equal(t, "solana", account.DestinationChain)
	require.Contains(t, account.RawPayload, "developer_fee_percent")
	require.NotNil(t, account.LastSyncedAt)
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
}

// Deactivation is the containment tool for this rail, so the read that follows
// it has to be able to close the account.
func TestProcessDeactivatesTheAccountFromTheReRead(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindVirtualAccount, EventID: "evt_1",
		EventType: "virtual_account.activity.deactivation", ProviderAccountID: "va_1",
		ExternalCustomerID: "cus_1",
		Account:            &ramp.VirtualAccount{ProviderAccountID: "va_1", Status: models.VirtualAccountStatusInactive},
	}, nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").Return(
		&ramp.VirtualAccount{ProviderAccountID: "va_1", Status: models.VirtualAccountStatusInactive}, nil).Once()
	h.db.EXPECT().FindVirtualAccountForUpdate(account.ID).Return(account, nil).Once()
	h.db.EXPECT().UpdateVirtualAccount(account).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.False(t, account.IsActive())
	require.Equal(t, "So1Wa11et", account.DestinationAddress, "a read that reports no destination clears none")
}

func TestProcessAppliesSettlementFieldsFromTransferWebhook(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusProcessing)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventID: "evt_1", ProviderOrderID: "trf_1",
		Status: models.TxStatusCompleted, TxHash: "0xabc",
		ExchangeRate: "5.405405", SourceAmount: "100.00", DestinationAmount: "18.500000",
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "0xabc", *tx.DepositTxSignature)
	require.Equal(t, "5.405405", *tx.ExchangeRate)
	require.Equal(t, "100.00", tx.AmountFiat)
	require.Equal(t, "18.500000", tx.AmountToken)
}

func TestProcessAppliesARampStatusAndBroadcasts(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusAwaitingFunds)
	occurred := time.Now()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventID: "evt_1", ProviderOrderID: "trf_1",
		Status: models.TxStatusCompleted, TxHash: "0xabc", OccurredAt: &occurred,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "0xabc", *tx.DepositTxSignature)
	require.NotNil(t, tx.CompletedAt, "completion is stamped once, from the event's own time")
	require.NotNil(t, row.ProcessedAt)
	require.Nil(t, row.ProcessingError)
	require.Equal(t, tx.ID, *row.TransactionID)
}

// A late or duplicated delivery must not walk a finished order backwards.
func TestProcessIgnoresARegressionOnATerminalOrder(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusCompleted)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, ProviderOrderID: "trf_1", Status: models.TxStatusProcessing,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A refund is a real transition out of a terminal state, not a late duplicate.
func TestProcessAllowsTheRefundPathOutOfTerminal(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusCompleted)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, ProviderOrderID: "trf_1", Status: models.TxStatusRefunded,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusRefunded, tx.Status)
}

// KYC events name the provider's customer, not our user. The integration row
// is the only thing that bridges the two.
func TestProcessResolvesAKycEventThroughTheIntegration(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	external := "cus_1"
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusUnderReview,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: external, Status: models.KycStatusApproved,
		Endorsements: []models.Endorsement{{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved}},
		Raw:          json.RawMessage(`{"id":"cus_1","tax_id":"39053344705"}`),
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, external).Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.KycStatus == models.KycStatusApproved
	})).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []string{models.KycStatusApproved}, h.verdicts, "the devices hear the verdict")
	require.True(t, kyc.SupportsRail(models.RailPixPayin), "the endorsement is what opens the rail")
	require.Contains(t, kyc.RawStatus, "39053344705", "the snapshot is persisted for the admin")
	require.NotNil(t, kyc.LastSyncedAt)
}

// A company's KYC event is the company's: its row is the one the integration
// names, and every user who acts for it hears of it, the company named.
func TestProcessTellsEveryMemberOfACompanyAboutItsKyc(t *testing.T) {
	const company = uint(1_000_000_000_000)
	h := newHarness(t)
	h.withMembers(t, company, 7, 9)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: company, Provider: models.ProviderBridge, KycStatus: models.KycStatusUnderReview,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}
	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_company", Status: models.KycStatusApproved,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_company").Return(
		&models.CustomerProviderIntegration{CustomerID: company, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(company, models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	namesTheCompany := testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
		return e.CustomerID == company && e.KycStatus == models.KycStatusApproved
	})
	h.broadcaster.EXPECT().PublishKyc(uint(7), namesTheCompany).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(9), namesTheCompany).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
}

// An unchanged status is not news, and a reconciliation sweep would otherwise
// wake every idle app.
func TestProcessDoesNotBroadcastAnUnchangedKycStatus(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusApproved,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusApproved,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	h.broadcaster.AssertNotCalled(t, "PublishKyc", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.verdicts, "an unchanged status is no news to the devices either")
}

// The failure is recorded on the row rather than only in a queue log, so an
// operator can see it. It is not pickable by webhooks:reprocess: stamp sets
// ProcessedAt on failure too, and FindUnprocessed filters processed_at IS NULL.
func TestProcessRecordsTheFailureOnTheEventRow(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, ProviderOrderID: "trf_missing",
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_missing").Return(nil, nil).Once()
	// An id that matches neither movement. The delivery lookup is the second
	// question asked before the event is declared unattributable.
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "trf_missing").Return(nil, nil).Once()
	// A failed apply rolls back, so its stamp is written on its own rather than
	// inside the transaction that just aborted.
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessedAt)
	require.NotNil(t, row.ProcessingError)
}

// A lock that cannot be taken is stamped with the processor's context, the text
// the admin's webhook page shows for the event.
func TestProcessStampsALockFailureAsSuch(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	h.events.EXPECT().FindByID(eventRowID).Return(row, nil).Once()
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Once()
	h.events.EXPECT().WithLocked(eventRowID, testifymock.Anything).
		Return(&repositories.LockError{Err: errors.New("webhook event repo: find by id for update: timeout")}).Once()
	h.lockedOnce(row)
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, "ramp webhook: lock event: webhook event repo: find by id for update: timeout", *row.ProcessingError)
}

// The same row reaches the processor more than once — the sweep, the queue's
// own retry and the admin explorer all dispatch the same event id — and a
// second apply moves the transaction again and tells the user a second time.
// The stamp is what says it is done, read under the lock inside the
// transaction so a concurrent run cannot pass the same guard.
func TestProcessAppliesAnEventOnlyOnce(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := ozavOnrampOrder(models.TxStatusAwaitingFunds)

	h.events.EXPECT().FindByID(eventRowID).Return(row, nil).Twice()
	h.locked(row).Twice()
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Twice()
	// Everything past the guard carries Once, so a second apply fails the test.
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.NoError(t, h.processor.Process(context.Background(), eventRowID), "a re-dispatch of an applied event is a no-op")
	require.Len(t, h.deliverer.delivered, 1, "the second run must not ask for the delivery again")
}

// A stamped *failure* is not an applied event. It is the row the admin
// explorer's retry exists for, and a guard reading only processed_at would
// turn that retry into a silent no-op.
func TestProcessReappliesAnEventWhoseLastRunFailed(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	stamped, failure := time.Now().Add(-time.Minute), "ramp webhook: load transaction: connection refused"
	row.ProcessedAt, row.ProcessingError = &stamped, &failure
	tx := rampOrder(models.TxStatusAwaitingFunds)

	h.events.EXPECT().FindByID(eventRowID).Return(row, nil).Once()
	h.locked(row).Once()
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Nil(t, row.ProcessingError, "the re-run that succeeds clears the failure")
}

// The entity write and the stamp commit together. Apart, an event whose stamp
// failed is re-dispatched by webhooks:reprocess and applied a second time — and
// the user is told twice. The broadcast waits for the commit for the same
// reason: announcing a state the rollback erases is worse than announcing it
// late.
func TestProcessDoesNotBroadcastWhenTheStampFails(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusAwaitingFunds)

	stored := eventRow()
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.lockedOnce(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, ProviderOrderID: "trf_1", Status: models.TxStatusCompleted,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(errors.New("deadlock detected")).Once()
	// The outcome is still recorded, so the operator can see what happened: on
	// the row as the rollback left it, read again under the lock.
	h.lockedOnce(stored)
	h.db.EXPECT().UpdateEvent(stored).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
	require.NotNil(t, stored.ProcessingError)
}

// The deposit branch is the only one that mints a transaction id *inside* the
// transaction. When the stamp then fails, the create rolls back with it — but
// the in-memory event outlives the rollback by design, and would carry the id
// of a row that was never committed into the failure stamp. webhook_events has
// no foreign key on transaction_id, so that would land as a silent orphan
// pointer the admin's webhook detail view resolves to nothing.
func TestProcessDoesNotStampATransactionIDTheRollbackErased(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()

	stored := eventRow()
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.lockedOnce(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(nil, nil).Once()
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(
		func(tx *models.Transaction) error {
			tx.ID = 42 // what the insert would assign, and the rollback take back
			return nil
		}).Once()
	h.db.EXPECT().UpdateEvent(row).Return(errors.New("deadlock detected")).Once()

	h.lockedOnce(stored)
	h.db.EXPECT().UpdateEvent(stored).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, stored.ProcessingError)
	require.Nil(t, stored.TransactionID, "the row it pointed at was rolled back with the stamp")
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A link the row already carried is not the rollback's to erase: an operator
// reprocessing an applied event whose payload no longer decodes must not lose
// the transaction it resolved to.
func TestProcessKeepsATransactionIDTheRowAlreadyCarried(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	linked := uint(11)
	row.TransactionID = &linked

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("unknown envelope")).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, &linked, row.TransactionID)
}

// An authentic event in a category we do not model is acknowledged, not
// retried forever.
func TestProcessAcknowledgesAnUnmodelledEvent(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(&ramp.WebhookResult{Kind: ramp.WebhookKindUnknown, EventID: "evt_9"}, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Nil(t, row.ProcessingError)
}

// An account change is recorded even when its owner would fail the router's
// eligibility chain.
//
// Reading an account back is bookkeeping, not transacting. Gating it on "may
// this user transact" breaks it exactly when it matters: a deactivation
// arriving because the user's compliance state lapsed would be refused by that
// same lapse, and the row would keep saying `active` while the app handed out
// deposit instructions whose funds are returned to the sender.
//
// The harness wires no provider config, no integration and no KYC row at all —
// the chain cannot pass, and the change still lands.
func TestProcessRecordsAnAccountChangeWithoutTheEligibilityChain(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	account := virtualAccountRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindVirtualAccount, EventID: "evt_1",
		EventType: "virtual_account.activity.deactivation", ProviderAccountID: "va_1",
		ExternalCustomerID: "cus_1",
		Account:            &ramp.VirtualAccount{ProviderAccountID: "va_1"},
	}, nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").Return(
		&ramp.VirtualAccount{
			ProviderAccountID: "va_1", Status: models.VirtualAccountStatusInactive,
		}, nil).Once()
	h.db.EXPECT().FindVirtualAccountForUpdate(account.ID).Return(account, nil).Once()
	h.db.EXPECT().UpdateVirtualAccount(account).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.VirtualAccountStatusInactive, account.Status)
	require.Nil(t, row.ProcessingError)
}

// The customer id is what the read is addressed to, so an account event without
// one is refused rather than sent with an empty reference.
func TestProcessRefusesAnAccountEventWithNoCustomerID(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindVirtualAccount, EventID: "evt_1",
		EventType: "virtual_account.activity.deactivation", ProviderAccountID: "va_1",
		Account: &ramp.VirtualAccount{ProviderAccountID: "va_1"},
	}, nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(virtualAccountRow(), nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.Contains(t, *row.ProcessingError, "customer id")
}

// A closed payin rail stops the standing account from opening new orders.
//
// The event is still accepted and stamped processed: the money is in the
// account either way, the operator who closed the rail is the one who decides
// what happens to it, and refusing the callback would only make the provider
// redeliver it forever. CreateTransaction and Publish carry no expectation
// here, so the mock fails the test if either is reached.
func TestProcessDoesNotOpenATransactionWhenThePayinRailIsClosed(t *testing.T) {
	h := newHarnessWithFeatures(t, closedPayin(t))
	row := eventRow()
	account := virtualAccountRow()
	occurred := time.Now()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "100.0",
			OccurredAt: &occurred,
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID),
		"the delivery is accepted; refusing it would only make the provider redeliver forever")
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
	require.Nil(t, row.TransactionID, "no transaction was opened, so the event names none")
}

// The veto stops new business; it does not strand what is in flight. A deposit
// event advancing a transaction that already exists still lands with the rail
// closed — this is the half of the design most likely to be broken by a later
// refactor that moves the check somewhere blunter.
func TestProcessStillAdvancesAnExistingTransactionWhenThePayinRailIsClosed(t *testing.T) {
	h := newHarnessWithFeatures(t, closedPayin(t))
	row := eventRow()
	account := virtualAccountRow()
	existing := &models.Transaction{
		Model: orm.Model{ID: 42}, CustomerID: 7, UserID: 7, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_1", GatewayOrderID: "dep_1",
		DepositID: support.Ptr("dep_1"), Status: models.TxStatusFundsReceived,
		AmountFiat: "100.0", AmountToken: "0", TargetToken: "USDC",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusProcessing, SourceAmount: "100.0", DestinationAmount: "97.5",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_1").Return(existing, nil).Once()
	h.db.EXPECT().UpdateTransaction(existing).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusProcessing, existing.Status,
		"a closed rail must not freeze an order that already exists")
	require.Equal(t, "97.5", existing.AmountToken)
}

// --- on-ramp delivery trigger -----------------------------------------------

func transferEvent(orderID, status string) *ramp.WebhookResult {
	return &ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventID: "evt_1", EventType: "transfer.completed",
		ProviderOrderID: orderID, Status: status,
	}
}

// expectTransferEvent stubs the reads a ramp event walks through, so the four
// tests below differ only in the thing each is measuring.
func expectTransferEvent(h *harness, row *models.WebhookEvent, tx *models.Transaction, status string) {
	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", status), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()
}

// A settled pay-in is the moment the crypto exists in the provider's custody
// and not yet in the user's wallet. Nothing else in the system knows that
// moment has arrived, so the processor is what says the second hop is due.
func TestProcessAsksForDeliveryWhenAnOnrampPayInCompletes(t *testing.T) {
	h := newHarness(t)
	tx := ozavOnrampOrder(models.TxStatusAwaitingFunds)
	expectTransferEvent(h, eventRow(), tx, models.TxStatusCompleted)

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))

	require.Len(t, h.deliverer.delivered, 1, "the settled pay-in must ask for the delivery")
	require.Same(t, tx, h.deliverer.delivered[0])
}

// The event that ends an order is the one the user may not be watching for, so
// the devices hear it beside the live stream.
func TestProcessTellsTheDevicesAnOrderEnded(t *testing.T) {
	h := newHarness(t)
	tx := rampOrder(models.TxStatusAwaitingFunds)
	expectTransferEvent(h, eventRow(), tx, models.TxStatusFailed)

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []string{models.TxStatusFailed}, h.announced)
}

// Before the pay-in settles there is nothing in custody to forward, and asking
// would be asking the provider to send money it has not received.
func TestProcessDoesNotAskForDeliveryBeforeThePayInSettles(t *testing.T) {
	h := newHarness(t)
	tx := rampOrder(models.TxStatusAwaitingFunds)
	expectTransferEvent(h, eventRow(), tx, models.TxStatusProcessing)

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Empty(t, h.deliverer.delivered)
}

// An off-ramp's crypto leaves the user's wallet; there is no second hop to ask
// for, and asking would be a send with nothing behind it.
func TestProcessDoesNotAskForDeliveryOnAnOfframp(t *testing.T) {
	h := newHarness(t)
	tx := rampOrder(models.TxStatusAwaitingFunds)
	tx.Type = models.TransactionTypeOfframp
	expectTransferEvent(h, eventRow(), tx, models.TxStatusCompleted)

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Empty(t, h.deliverer.delivered)
}

// The pay-in did settle and the row must reflect it. A delivery that fails is
// the reconciler's problem, not a reason to leave the event unprocessed: a
// redelivery would re-apply a status that is already correct, and the delivery
// itself is guarded by delivery_order_id rather than by the event.
func TestProcessKeepsThePayInAppliedWhenTheDeliveryFails(t *testing.T) {
	h := newHarness(t)
	h.deliverer.err = errors.New("provider refused the send")
	tx := ozavOnrampOrder(models.TxStatusAwaitingFunds)
	expectTransferEvent(h, eventRow(), tx, models.TxStatusCompleted)

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Len(t, h.deliverer.delivered, 1)
}

// A read that re-checked the provider can complete an on-ramp before its event
// lands (refreshInFlight), and a read's delivery can fail. The event then finds
// the order already completed and changes nothing about it — but the crypto is
// still in the provider's custody, so it asks for the delivery again rather
// than leave it to the next reconcile sweep.
func TestProcessAsksForTheDeliveryOfAnOnrampAlreadyCompletedWithoutIt(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusCompleted)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Len(t, h.deliverer.delivered, 1, "the settled pay-in is still in custody")
	require.Same(t, tx, h.deliverer.delivered[0])
	h.db.AssertNotCalled(t, "UpdateTransaction", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// Once the delivery is placed a late event asks for nothing: the send is
// irreversible, and delivery_order_id is what says it went out.
func TestProcessDoesNotAskAgainForAnOnrampAlreadyDelivered(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusCompleted)
	tx.DeliveryOrderID = support.Ptr("ozav_tr_delivery_1")

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Empty(t, h.deliverer.delivered)
}

// The delivery is its own transfer upstream, with its own id, so its lifecycle
// events name something gateway_order_id never matches. Before this was
// handled every one of them failed Process with "no transaction for ozav order
// X" — a permanent processing error per event, and the only signal that a
// user's crypto never left the provider's custody.
func TestProcessMatchesADeliveryEventByItsOwnID(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusProcessing)
	tx.DeliveryOrderID = support.Ptr("trf_delivery")

	h.storedEvent(row)
	delivered := transferEvent("trf_delivery", models.TxStatusCompleted)
	delivered.TxHash = "5sig"
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
		Return(delivered, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_delivery").Return(nil, nil).Once()
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "trf_delivery").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status, "the pay-in's status is not the delivery's")
	require.Empty(t, h.deliverer.delivered, "a delivery event must not trigger another delivery")
}

// A delivery that failed upstream is the case that matters: the pay-in
// completed, the user paid, and the crypto did not reach them. It has to be
// recorded on the row rather than left as an unprocessable event.
func TestProcessRecordsAFailedDelivery(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusProcessing)
	tx.DeliveryOrderID = support.Ptr("trf_delivery")

	result := transferEvent("trf_delivery", models.TxStatusProcessing)
	result.DeliveryFailed = true
	result.FailureReason = "address rejected"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_delivery").Return(nil, nil).Once()
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "trf_delivery").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, tx.FailureReason)
	require.Contains(t, *tx.FailureReason, "delivery")
	require.Nil(t, tx.DeliveryOrderID)
	require.Equal(t, 1, tx.DeliveryAttempts)
	require.Equal(t, models.TxStatusProcessing, tx.Status,
		"the pay-in is untouched; it is the second hop that did not complete")
	require.Empty(t, h.announced, "the order's status stands, and the devices already heard how it ended")
}

// pending_steps, the redistribution filter and the screen the wizard opens are
// all derived from this column. The sync path writes it through applyKycState;
// the webhook path wrote neither it nor the rejection reasons, so a provider
// reporting new outstanding items left the row describing the previous sync.
func TestProcessPersistsRequirementsAndRejectionReasonsFromTheWebhook(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusUnderReview,
		Endorsements: "[]", Requirements: `["government_id"]`, RejectionReasons: "[]", RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusIncomplete,
		Requirements:        []string{"live_selfie"},
		RejectionReasons:    []string{"document unreadable"},
		ReportsRequirements: true,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	var published broadcast.KycEvent
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).
		Run(func(_ uint, e broadcast.KycEvent) { published = e }).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.JSONEq(t, `["live_selfie"]`, kyc.Requirements)
	require.JSONEq(t, `["document unreadable"]`, kyc.RejectionReasons)
	// The frame has to carry what it persisted. The sync path's frame does, and
	// an app that read one shape from one path and another from the other would
	// derive its wizard from whichever path happened to answer last.
	require.Equal(t, broadcast.KycEvent{
		CustomerID:       7,
		Provider:         models.ProviderBridge,
		KycStatus:        models.KycStatusIncomplete,
		Requirements:     []string{"live_selfie"},
		RejectionReasons: []string{"document unreadable"},
	}, published)
}

// "An unchanged status is not news" stopped being the whole truth once the
// client started deriving its wizard from requirements: a provider that holds
// the status and changes what it is asking for IS news.
func TestProcessBroadcastsWhenOnlyTheRequirementsChanged(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusIncomplete,
		Endorsements: "[]", Requirements: `["government_id"]`, RejectionReasons: "[]", RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1",
		Status:       models.KycStatusIncomplete, // unchanged on purpose
		Requirements: []string{"live_selfie"}, ReportsRequirements: true,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	var published broadcast.KycEvent
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).
		Run(func(_ uint, e broadcast.KycEvent) { published = e }).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	// The requirements change is the whole reason this frame went out, so a
	// frame that omits them announces nothing the app can act on.
	require.Equal(t, []string{"live_selfie"}, published.Requirements)
	require.Empty(t, published.RejectionReasons)
}

// Not every adapter speaks this vocabulary. Ozav's customer events carry a
// status and an endorsement but say nothing about requirements, so writing the
// decoded value unconditionally emptied the column on every delivery — which
// blanks pending_steps, leaves the wizard with no screen to open, and makes the
// redistribution filter skip the provider for good. Silence is not "owes
// nothing"; only a decoder that read the field may clear it.
func TestProcessLeavesRequirementsAloneWhenTheEventDoesNotCarryThem(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusUnderReview,
		Endorsements: "[]", Requirements: `["government_id"]`,
		RejectionReasons: `["cannot validate id"]`, RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusIncomplete,
		// ReportsRequirements is false: the decoder never looked.
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.JSONEq(t, `["government_id"]`, kyc.Requirements,
		"the event said nothing about requirements; the row must keep what the sync knew")
	require.JSONEq(t, `["cannot validate id"]`, kyc.RejectionReasons)
}

// A decoder that did read the field must still be able to report an empty one:
// an approved customer owes nothing, and bridge's requirements() returns nil
// for exactly that case. The flag, not the nil-ness of the slice, is what
// separates "owes nothing" from "was never asked".
func TestProcessClearsRequirementsWhenTheEventReportsNone(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusIncomplete,
		Endorsements: "[]", Requirements: `["government_id"]`, RejectionReasons: "[]", RawStatus: "{}",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusApproved,
		Requirements: nil, ReportsRequirements: true,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.JSONEq(t, `[]`, kyc.Requirements)
}

// The rejection categories travel with the status that caused them: an event
// that states a status states the whole posture, so it writes both columns —
// and broadcasts, since a new reason to be refused is news even when the
// status stays rejected.
func TestProcessPersistsTheRejectionCategoriesFromTheWebhook(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusRejected,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
		RejectionCategories: `["selfie_mismatch"]`, RejectionDetails: "[]",
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusRejected,
		RejectionCategories: []string{models.KycRejectionTaxIDInUse},
		RejectionDetails:    []string{"Another customer with this tax ID number already exists."},
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []string{models.KycRejectionTaxIDInUse}, kyc.DecodedRejectionCategories())
	require.Equal(t, []string{"Another customer with this tax ID number already exists."}, kyc.DecodedRejectionDetails())
	require.True(t, kyc.RejectionTerminal())
}

// A status that is not a rejection carries no category, and the old one must
// go: a customer approved after a selfie resubmission is not still refused.
func TestProcessClearsTheRejectionCategoriesWhenTheStatusMovesOn(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusRejected,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
		RejectionCategories: `["selfie_mismatch"]`, RejectionDetails: `["portrait mismatch"]`,
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusApproved,
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.JSONEq(t, `[]`, kyc.RejectionCategories)
	require.JSONEq(t, `[]`, kyc.RejectionDetails)
}

// An event with no status (an Ozav verification session event) says nothing
// about the customer's posture, so it leaves the categories as they were.
func TestProcessLeavesTheRejectionCategoriesAloneWithoutAStatus(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	kyc := &models.CustomerProviderKyc{
		CustomerID: 7, Provider: models.ProviderBridge, KycStatus: models.KycStatusRejected,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RawStatus: "{}",
		RejectionCategories: `["not_eligible"]`, RejectionDetails: `["sanctions_match"]`,
	}

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1",
	}, nil).Once()
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(
		&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}, nil).Once()
	h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(kyc, nil).Once()
	h.db.EXPECT().UpdateKyc(kyc).Return(nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.JSONEq(t, `["not_eligible"]`, kyc.RejectionCategories)
	require.JSONEq(t, `["sanctions_match"]`, kyc.RejectionDetails)
}

// A run whose apply fails stamps the failure after its transaction rolled
// back, and a duplicate run may have applied the event in between. The failed
// run must not write its stamp, or its pre-apply link, over that success: the
// event would then read as failed and unlinked although it was applied.
func TestAFailedRunLeavesAnEventADuplicateAppliedAlone(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	linked := uint(99)
	applied := eventRow()
	processed := time.Now()
	applied.ProcessedAt = &processed
	applied.TransactionID = &linked

	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventID: "evt_1", ProviderOrderID: "trf_1", Status: models.TxStatusCompleted,
	}, nil).Once()
	// This run's apply: the order read fails and the transaction rolls back.
	h.lockedOnce(row)
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(nil, errors.New("deadlock detected")).Once()
	// Meanwhile a duplicate run applied the event and committed; the stamp
	// re-reads it under the lock.
	h.lockedOnce(applied)

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	h.db.AssertNotCalled(t, "UpdateEvent", testifymock.Anything)
	require.Nil(t, applied.ProcessingError)
	require.Equal(t, &linked, applied.TransactionID)
}

// With no order, customer or deposit id there is nothing to key a write on.
func TestProcessAcknowledgesAnEventThatNamesNothing(t *testing.T) {
	cases := map[string]*ramp.WebhookResult{
		"order":   {Kind: ramp.WebhookKindRamp, Status: models.TxStatusCompleted},
		"kyc":     {Kind: ramp.WebhookKindKyc, Status: models.KycStatusApproved},
		"deposit": depositEvent(&ramp.VirtualAccountDeposit{ProviderAccountID: "va_1"}),
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			row := eventRow()
			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.NoError(t, h.processor.Process(context.Background(), eventRowID))
			require.NotNil(t, row.ProcessedAt)
			require.Nil(t, row.ProcessingError)
		})
	}
}

// Only the integration row ties a provider customer to a user. A customer XIP
// detached is acknowledged instead (TestProcessAcknowledgesAKycEventForADetachedCustomer).
func TestProcessRefusesAKycEventItCannotAttribute(t *testing.T) {
	cases := []struct {
		name string
		link *models.CustomerProviderIntegration
	}{
		{name: "no user linked to the customer"},
		{name: "a linked user without a kyc row", link: &models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			row := eventRow()
			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
				Kind: ramp.WebhookKindKyc, ExternalCustomerID: "cus_1", Status: models.KycStatusApproved,
			}, nil).Once()
			h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderBridge, "cus_1").Return(c.link, nil).Once()
			if c.link != nil {
				h.db.EXPECT().FindKycForUpdate(uint(7), models.ProviderBridge).Return(nil, nil).Once()
			} else {
				h.integRepo.EXPECT().IsDetachedCustomer(models.ProviderBridge, "cus_1").Return(false, nil).Once()
			}
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.Error(t, h.processor.Process(context.Background(), eventRowID))
			require.NotNil(t, row.ProcessingError)
		})
	}
}

func TestProcessRecordsTheDeliveryHashAndTheProvidersReason(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusCompleted)
	result := transferEvent("trf_delivery", models.TxStatusFailed)
	result.TxHash = "0xdead"
	result.FailureReason = "address rejected"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_delivery").Return(nil, nil).Once()
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "trf_delivery").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, "0xdead", *tx.DepositTxSignature)
	require.Equal(t, models.TxStatusCompleted, tx.Status,
		"the pay-in status is unchanged when delivery did not reach terminal success")
	require.Nil(t, tx.FailureReason)
}

func TestProcessRecordsTheReasonAnOrderFailed(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusProcessing)
	result := transferEvent("trf_1", models.TxStatusFailed)
	result.FailureReason = "pix rejected"

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusFailed, tx.Status)
	require.Equal(t, "pix rejected", *tx.FailureReason)
}

func TestProcessUnknownEvent(t *testing.T) {
	t.Run("not stored", func(t *testing.T) {
		h := newHarness(t)
		h.events.EXPECT().FindByID(eventRowID).Return(nil, nil).Once()

		require.ErrorIs(t, h.processor.Process(context.Background(), eventRowID), rampsvc.ErrEventNotFound)
	})
	t.Run("gone under the lock", func(t *testing.T) {
		h := newHarness(t)
		row := eventRow()
		h.events.EXPECT().FindByID(eventRowID).Return(row, nil).Once()
		h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).
			Return(transferEvent("trf_1", models.TxStatusCompleted), nil).Once()
		h.events.EXPECT().WithLocked(eventRowID, testifymock.Anything).
			RunAndReturn(func(_ uint, fn func(*models.WebhookEvent, repositories.WebhookTx) error) error {
				return fn(nil, h.db)
			}).Twice()

		require.ErrorIs(t, h.processor.Process(context.Background(), eventRowID), rampsvc.ErrEventNotFound)
	})
}

func TestProcessRefusesAnAccountEventItCannotResolve(t *testing.T) {
	cases := []struct {
		name   string
		expect func(h *harness, account *models.CustomerVirtualAccount, result *ramp.WebhookResult)
	}{
		{name: "it names no account", expect: func(_ *harness, _ *models.CustomerVirtualAccount, result *ramp.WebhookResult) {
			result.ProviderAccountID = ""
		}},
		{name: "the provider reads back nothing", expect: func(h *harness, account *models.CustomerVirtualAccount, _ *ramp.WebhookResult) {
			h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
			h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").Return(nil, nil).Once()
		}},
		{name: "the account is gone under the lock", expect: func(h *harness, account *models.CustomerVirtualAccount, _ *ramp.WebhookResult) {
			h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(account, nil).Once()
			h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
				Return(&ramp.VirtualAccount{ProviderAccountID: "va_1"}, nil).Once()
			h.db.EXPECT().FindVirtualAccountForUpdate(account.ID).Return(nil, nil).Once()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			row := eventRow()
			result := &ramp.WebhookResult{
				Kind: ramp.WebhookKindVirtualAccount, EventID: "evt_1", ProviderAccountID: "va_1", ExternalCustomerID: "cus_1",
			}
			c.expect(h, virtualAccountRow(), result)
			h.storedEvent(row)
			h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
			h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

			require.Error(t, h.processor.Process(context.Background(), eventRowID))
			require.NotNil(t, row.ProcessingError)
		})
	}
}

// A locally expired on-ramp whose provider later reports completion must heal.
func TestProcessHealsALocallyExpiredOnrampWhenTheProviderReportsCompletion(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusExpired)

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventType: "payin.complete",
		ProviderOrderID: "trf_1", Status: models.TxStatusCompleted, TxHash: "5sig",
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusCompleted, tx.Status)
	require.Equal(t, "5sig", *tx.DepositTxSignature)
	require.True(t, tx.AwaitsDelivery())
	require.Equal(t, []*models.Transaction{tx}, h.deliverer.delivered, "the healed on-ramp's second hop is asked for")
}

// Processing is every adapter's fallback for a status it cannot name (an Ozav
// needs_review, a new Bridge state). A late one must not revive an order that
// expired unfunded: the row stays expired, nothing is persisted or announced.
func TestProcessKeepsAnExpiredOfframpExpiredOnALateProcessingEvent(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusExpired)
	tx.Type = models.TransactionTypeOfframp

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventType: "transfer.updated",
		ProviderOrderID: "trf_1", Status: models.TxStatusProcessing,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_1").Return(tx, nil).Once()
	// No UpdateTransaction and no broadcast are expected: the mocks fail on
	// any call that was not set up.
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.TxStatusExpired, tx.Status)
	require.Empty(t, h.announced)
}

// A BlindPay payin event can beat the transaction's own insert, or name a payin
// no transaction has. It must be neither applied nor dropped: the apply fails,
// the row keeps its error and no processed-and-applied stamp, so reprocessing
// picks it up once the transaction exists. The processor is provider-agnostic;
// the harness registers its one mock adapter under bridge.
func TestProcessLeavesABlindPayPayinEventForAnUnknownOrderReprocessable(t *testing.T) {
	h := newHarness(t)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindRamp, EventType: "payin.update",
		ProviderOrderID: "pi_unknown00001", Status: models.TxStatusProcessing,
	}, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "pi_unknown00001").Return(nil, nil).Once()
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "pi_unknown00001").Return(nil, nil).Once()
	// No UpdateTransaction and no broadcast are expected: the mocks fail on
	// any call that was not set up.
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.False(t, row.Applied(), "an unattributed event stays reprocessable")
}

// usdVirtualAccountRow is the same standing account in dollars.
func usdVirtualAccountRow() *models.CustomerVirtualAccount {
	account := virtualAccountRow()
	account.SourceCurrency = "usd"
	account.DepositInstructions = `{"currency":"usd","bank_routing_number":"101019644"}`
	return account
}

// A deposit into a USD account settles on usd_payin, in dollars, and names the
// bank network it came on. Before the rails were per currency it was stamped
// pix_payin and vetoed by the Pix switch.
func TestProcessCreatesAUSDTransactionFromAUSDDeposit(t *testing.T) {
	features := mocks.NewMockFeature(t)
	features.EXPECT().IsActive(testifymock.Anything, ramp.RailUSDPayin.Flag(), models.UserFlagScope(7)).
		Return(true, nil).Once()
	h := newHarnessWithFeatures(t, features)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_usd", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "120.0",
			FeeAmount: "1.2", SenderName: "Acme Payroll LLC", PaymentRail: "ach",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(usdVirtualAccountRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_usd").Return(nil, nil).Once()

	var created *models.Transaction
	h.db.EXPECT().CreateTransaction(testifymock.Anything).RunAndReturn(
		func(tx *models.Transaction) error {
			tx.ID = 43
			created = tx
			return nil
		}).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, models.RailUSDPayin, *created.Rail)
	require.Equal(t, "usd", created.FiatCurrency)
	require.Equal(t, "120.0", created.AmountFiat, "amount_fiat holds the fiat leg, in fiat_currency")
	require.Equal(t, "usd", *created.FeeCurrency)
	require.Equal(t, "ach", *created.PaymentRail)
	require.Equal(t, "Acme Payroll LLC", *created.SenderName)
}

// The USD switch closes the USD deposit; the Pix switch does not.
func TestProcessDoesNotOpenAUSDTransactionWhenTheUSDPayinRailIsClosed(t *testing.T) {
	features := openFeatures()
	require.NoError(t, features.Deactivate(context.Background(), ramp.RailUSDPayin.Flag(), nil))
	h := newHarnessWithFeatures(t, features)
	row := eventRow()

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(
		depositEvent(&ramp.VirtualAccountDeposit{
			DepositID: "dep_usd", ProviderAccountID: "va_1",
			Status: models.TxStatusFundsReceived, SourceAmount: "120.0",
		}), nil).Once()
	h.accounts.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "va_1").Return(usdVirtualAccountRow(), nil).Once()
	h.db.EXPECT().FindDepositForUpdate(models.ProviderBridge, "dep_usd").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	h.db.AssertNotCalled(t, "CreateTransaction", testifymock.Anything)
}

// The provider can make the transfer and still answer its create with a
// refusal (Ozav, 02/11/2026: AMOUNT_BELOW_MINIMUM on POST /transfers, then
// transfer.failed for the transfer it had made). gateway_order_id was never
// written, so the event finds its order by the quote it was made from: it
// links the transfer and applies the outcome instead of being unattributable.
func TestProcessFindsAnUnplacedOrderByItsQuote(t *testing.T) {
	h := newHarness(t)
	row := eventRow()
	tx := rampOrder(models.TxStatusAwaitingFunds)
	tx.Type = models.TransactionTypeOfframp
	tx.GatewayOrderID = ""

	result := transferEvent("trf_unplaced", models.TxStatusFailed)
	result.ProviderQuoteID = "q_1"
	result.FailureReason = "AMOUNT_BELOW_MINIMUM: Minimum amount for this off-ramp is 185 USDC."

	h.storedEvent(row)
	h.adapter.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(result, nil).Once()
	h.db.EXPECT().FindOrderForUpdate(models.ProviderBridge, "trf_unplaced").Return(nil, nil).Once()
	h.db.EXPECT().FindDeliveryForUpdate(models.ProviderBridge, "trf_unplaced").Return(nil, nil).Once()
	h.db.EXPECT().FindUnplacedOrderByQuoteForUpdate(models.ProviderBridge, "q_1").Return(tx, nil).Once()
	h.db.EXPECT().UpdateTransaction(tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(uint(7), testifymock.Anything).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, "trf_unplaced", tx.GatewayOrderID)
	require.Equal(t, models.TxStatusFailed, tx.Status)
	require.Equal(t, result.FailureReason, *tx.FailureReason)
	require.Equal(t, &tx.ID, row.TransactionID)
}
