package ramp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// reconcilerHarness wires the reconciler over the real router, so the
// eligibility chain (rail order → provider config → integration → endorsements)
// runs for real. That chain is what decides whether an order can be asked about
// at all, and stubbing it would leave the sweep untested where it matters.
type reconcilerHarness struct {
	reconciler  *rampsvc.Reconciler
	deliverer   *recordingDeliverer
	adapter     *mocks.MockProviderAdapter
	standing    *mocks.MockVirtualAccountAdapter
	settings    *mocks.MockSettingsSource
	integ       *mocks.MockCustomerProviderIntegrationRepository
	kycs        *mocks.MockCustomerProviderKycRepository
	txRepo      *mocks.MockTransactionRepository
	accounts    *mocks.MockCustomerVirtualAccountRepository
	broadcaster *mocks.MockBroadcaster
	settler     *mocks.MockOfframpSettler
	notifier    *mocks.MockNotifier
	// announced is the order statuses the devices were told about, in order.
	announced []string
	// bindings is the stored binding of each bank payout, and boundReads the
	// ids the provider was asked to read bound.
	kycRow     *models.CustomerProviderKyc
	bindings   *stubBindings
	boundReads []string
	bound      func(id string, binding ramp.OrderBinding) (*ramp.RampOrder, error)
	// attention is the stand-in for the mark's row: the code each transaction
	// holds, and how many times MarkAttention was asked to write it. markErr and
	// clearErr fail the calls.
	attention            map[uint]string
	markCalls, markWrote int
	clearCalls           int
	markErr, clearErr    error
}

const reconcileUser = uint(7)

// newReconcilerHarness wires a provider that finishes its on-ramp in one hop,
// which is the common case and keeps the delivery sweep out of every other
// test's expectations.
func newReconcilerHarness(t *testing.T) *reconcilerHarness {
	return newReconcilerHarnessWith(t, false)
}

// newDeliveringReconcilerHarness wires one that needs the second hop, so the
// sweep's capability probe finds it.
func newDeliveringReconcilerHarness(t *testing.T) *reconcilerHarness {
	return newReconcilerHarnessWith(t, true)
}

func newReconcilerHarnessWith(t *testing.T, delivers bool) *reconcilerHarness {
	return newReconcilerHarnessFor(t, delivers, bankNone)
}

// bankMode says whether the provider also reads bank payouts bound to a stored
// binding, and whether the harness holds the binding source for them.
type bankMode int

const (
	bankNone bankMode = iota
	// bankBound is a provider with the BoundOrderReader port.
	bankBound
	// bankUnbound is a provider that has no such port.
	bankUnbound
)

// boundReaderAdapter is a provider that also reads a payout against its binding.
type boundReaderAdapter struct {
	*standingAdapter
	read func(ctx context.Context, id string, binding ramp.OrderBinding) (*ramp.RampOrder, error)
}

func (a *boundReaderAdapter) GetBoundOrder(
	ctx context.Context, _ ramp.CustomerRef, id string, binding ramp.OrderBinding,
) (*ramp.RampOrder, error) {
	return a.read(ctx, id, binding)
}

// stubBindings is the stored binding by transaction id.
type stubBindings struct {
	byTx map[uint]*ramp.OrderBinding
	err  error
}

func (b *stubBindings) PayoutBinding(txID uint) (*ramp.OrderBinding, error) {
	return b.byTx[txID], b.err
}

func newReconcilerHarnessFor(t *testing.T, delivers bool, bank bankMode) *reconcilerHarness {
	t.Helper()
	newHarness(t) // binds the Log facade the sweep writes to

	h := &reconcilerHarness{
		adapter:     mocks.NewMockProviderAdapter(t),
		standing:    mocks.NewMockVirtualAccountAdapter(t),
		settings:    mocks.NewMockSettingsSource(t),
		integ:       mocks.NewMockCustomerProviderIntegrationRepository(t),
		kycs:        mocks.NewMockCustomerProviderKycRepository(t),
		txRepo:      mocks.NewMockTransactionRepository(t),
		accounts:    mocks.NewMockCustomerVirtualAccountRepository(t),
		broadcaster: mocks.NewMockBroadcaster(t),
		settler:     mocks.NewMockOfframpSettler(t),
		notifier:    mocks.NewMockNotifier(t),
	}
	h.attention = map[uint]string{}
	h.txRepo.EXPECT().MarkAttention(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(id uint, code string, _ time.Time) (bool, error) {
			h.markCalls++
			if h.markErr != nil {
				return false, h.markErr
			}
			if h.attention[id] == code {
				return false, nil
			}
			h.attention[id] = code
			h.markWrote++
			return true, nil
		}).Maybe()
	h.txRepo.EXPECT().ClearAttention(testifymock.Anything).RunAndReturn(func(id uint) (bool, error) {
		h.clearCalls++
		if h.clearErr != nil {
			return false, h.clearErr
		}
		_, had := h.attention[id]
		delete(h.attention, id)
		return had, nil
	}).Maybe()
	h.notifier.EXPECT().TransactionUpdated(testifymock.Anything).Run(func(tx *models.Transaction) {
		h.announced = append(h.announced, tx.Status)
	}).Maybe()
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.adapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	railGroup := ramp.RailGroup(ramp.RailPixPayin)
	h.settings.EXPECT().String(railGroup, ramp.SettingProviderOrder).Return(models.ProviderBridge, nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("1.50", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("1.50", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBlindPay)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMaxAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()

	providerGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(providerGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(providerGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	h.integ.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(
		&models.CustomerProviderIntegration{
			CustomerID: reconcileUser, Provider: models.ProviderBridge,
			Status: models.IntegrationStatusActive, ExternalCustomerID: support.Ptr("cus_1"),
			Metadata: "{}",
		}, nil).Maybe()

	approved := &models.CustomerProviderKyc{
		CustomerID: reconcileUser, Provider: models.ProviderBridge, KycStatus: models.KycStatusApproved,
	}
	require.NoError(t, approved.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
		{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusApproved},
	}))
	h.kycRow = approved
	h.kycs.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(approved, nil).Maybe()

	// The provider serves the rail both ways — the fat ProviderAdapter the sweep
	// resolves, and the standing-account port the deposit branch type-asserts off
	// it — exactly as Bridge does.
	standing := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	var provider ramp.ProviderAdapter = standing
	if delivers {
		provider = &deliveringProviderAdapter{standingAdapter: standing}
	}
	factory := ramp.NewFactory(provider)
	h.bindings = &stubBindings{byTx: map[uint]*ramp.OrderBinding{}}
	if bank == bankBound {
		bankProvider := &boundReaderAdapter{standingAdapter: standing, read: func(_ context.Context, id string, b ramp.OrderBinding) (*ramp.RampOrder, error) {
			h.boundReads = append(h.boundReads, id)
			return h.bound(id, b)
		}}
		factory = ramp.NewFactory(bankProvider)
	}
	router := ramp.NewRouter(ramp.RouterDeps{Factory: factory, Config: ramp.NewConfig(h.settings),
		Integrations: h.integ, Kyc: h.kycs, Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: mocks.NewMockCustomerIndividualProfileRepository(t)}, AppEnv: "local"})
	h.deliverer = &recordingDeliverer{}
	h.reconciler = rampsvc.NewReconciler(rampsvc.ReconcilerDeps{
		Bindings: h.bindings,
		Router:   router, Factory: factory, Integrations: h.integ, Transactions: h.txRepo,
		Accounts: h.accounts, Audience: broadcast.NewAudience(h.broadcaster, support.IndividualMembers(t)),
		Notifier: h.notifier, Config: ramp.NewConfig(h.settings),
		Deliverer: h.deliverer, Settler: h.settler,
		Addresses: mocks.NewMockUserLiquidationAddressRepository(t),
	})
	return h
}

func staleOrder(id uint, status string, expiresAt *time.Time, gatewayOrderID string) models.Transaction {
	rail := models.RailPixPayin
	return models.Transaction{
		Model: orm.Model{ID: id}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_" + rail,
		GatewayOrderID: gatewayOrderID, Status: status, Rail: &rail,
		AmountFiat: "100.00", AmountToken: "18.22", TargetToken: "usdc",
		ExpiresAt: expiresAt,
	}
}

// staleDeposit is an order the deposit webhook created: the money arrived
// before the row existed, so it carries a virtual account, the deposit id in
// GatewayOrderID and — the fact the sweep turns on — no expiry.
func staleDeposit(id uint, status, depositID string) models.Transaction {
	tx := staleOrder(id, status, nil, depositID)
	accountID := uint(3)
	tx.VirtualAccountID = &accountID
	tx.DepositID = &depositID
	tx.TargetToken = "USDC"
	tx.AmountToken = "0"
	// The deposit webhook that created this row already ran it through
	// ApplyDeposit once, which backfills the fiat currency off the account —
	// see ApplyDeposit's note on FeeCurrency. A fixture that omitted it would
	// have the sweep's first read look like a change on every deposit row,
	// which is not what re-reading an already-processed row looks like in
	// production.
	tx.FeeCurrency = support.Ptr("brl")
	return tx
}

func past() *time.Time  { t := time.Now().Add(-time.Hour); return &t }
func later() *time.Time { t := time.Now().Add(time.Hour); return &t }

// An order nobody funded before its window closed is expired locally. The
// deposit window belongs to the instructions we handed the user, so this does
// not wait for the provider to agree: the provider is asked once, and an
// unfunded answer leaves the decision as it was.
func TestSweepExpiresAnUnfundedOrder(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusAwaitingFunds}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(true, &saved)).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Scanned)
	require.Equal(t, 1, report.Expired)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, models.TxStatusExpired, saved.Status)
	require.Equal(t, []string{models.TxStatusExpired}, h.announced, "the devices hear the window closed")
}

// A payment that landed just before the window closed, whose event is late or
// lost, is applied rather than expired: an expired row is never swept again,
// so expiring it here would leave it wrong for good.
func TestSweepAppliesFundsTheProviderReportsInsteadOfExpiring(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusFundsReceived}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusAwaitingFunds).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, models.TxStatusFundsReceived, saved.Status)
	require.Equal(t, []string{models.TxStatusFundsReceived}, h.announced, "the devices hear the funds, never an expiry")
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
}

// A provider that cannot be read leaves the local decision standing: the
// window closed unfunded as far as anyone can tell.
func TestSweepExpiresWhenTheProviderCannotBeReadAtTheWindow(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(nil, errors.New("bridge: GET /transfers/trf_1: 503")).Once()
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(true, nil)).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Expired)
	require.Equal(t, 1, report.Failed, "the unreadable provider is counted even though the order expires")
	require.Equal(t, []string{models.TxStatusExpired}, h.announced)
}

// A webhook that applied funds while the provider was being read wins: the
// expiry write is conditional, finds the row moved, and neither announces nor
// counts an expiry.
func TestSweepDoesNotExpireAnOrderAWebhookFundedMeanwhile(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusAwaitingFunds}, nil).Once()
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(false, nil)).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 0, report.Failed)
	require.Empty(t, h.announced)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A refresh at the window that finds the order already settled is an update,
// announced once, never an expiry.
func TestSweepCountsATerminalRefreshAtTheWindowAsUpdatedNotExpired(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted, TxHash: "0xabc"}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusAwaitingFunds).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, []string{models.TxStatusCompleted}, h.announced)
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
}

// A refresh at the window that moves the order to processing leaves it alone:
// the provider has it, so the window no longer decides.
func TestSweepDoesNotExpireAnOrderTheProviderReportsProcessing(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusProcessing}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusAwaitingFunds).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, []string{models.TxStatusProcessing}, h.announced)
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
}

// An Ozav off-ramp with a recorded deposit hash must not be expired locally:
// the funds are already in provider custody even if the quote window lapsed.
func TestSweepDoesNotExpireOzavOfframpWithDepositSignature(t *testing.T) {
	h := newReconcilerHarness(t)
	sig := "5xDepositSig"
	rail := models.RailPixPayout
	order := models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderOzav,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_off", GatewayOrderID: "",
		Status: models.TxStatusAwaitingFunds, Rail: &rail, ExpiresAt: past(),
		DepositTxSignature: &sig,
	}

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	// The balance has not caught up with the claimed deposit yet; the hash
	// alone keeps the order open.
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).Return(false, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)
}

// Once money has arrived the window is no longer the deciding fact. Marking it
// expired would contradict funds the provider is already holding.
func TestSweepDoesNotExpireAFundedOrder(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusFundsReceived, past(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusFundsReceived}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 0, report.Updated, "an unchanged status writes nothing")
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// The whole point of the sweep: a webhook that never arrived leaves an order
// stuck, and the read against the provider resolves it.
func TestSweepAdoptsTheProviderStatusAndBroadcasts(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted, TxHash: "0xabc"}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, models.TxStatusCompleted, saved.Status)
	require.Equal(t, "0xabc", *saved.DepositTxSignature)
	require.NotNil(t, saved.CompletedAt, "completion is stamped once")
	require.Equal(t, []string{models.TxStatusCompleted}, h.announced, "the devices hear what the sweep found")
}

// A late delivery must not walk a finished order backwards, and the sweep is
// just another delivery.
func TestSweepDoesNotRegressATerminalOrder(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusCompleted, later(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusProcessing}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
}

// A reservation row — written before the create and never answered — carries no
// gateway order id. There is nothing to ask the provider about under an id it
// never issued, so expiry is what resolves it.
func TestSweepExpiresAReservationTheProviderNeverAnswered(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusPending, past(), "")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(true, &saved)).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Expired)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// One provider being down must not abort the rest of the batch: the orders
// behind it would sit unresolved until the failing one recovered.
func TestSweepContinuesPastAProviderFailure(t *testing.T) {
	h := newReconcilerHarness(t)
	failing := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_bad")
	healthy := staleOrder(2, models.TxStatusAwaitingFunds, later(), "trf_ok")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{failing, healthy}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_bad").
		Return(nil, errors.New("bridge: GET /transfers/trf_bad: 503")).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_ok").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 2, report.Scanned)
	require.Equal(t, 1, report.Failed)
	require.Equal(t, 1, report.Updated)
}

// Resolution is memoized for the length of one sweep. Without it a batch of N
// orders from the same user costs 3N eligibility queries where 3 would do.
func TestSweepResolvesTheProviderOncePerUserAndRail(t *testing.T) {
	h := newReconcilerHarness(t)
	// The .Maybe() expectations in the harness allow any number of calls; these
	// replace them with exact counts.
	h.settings.ExpectedCalls = nil
	h.integ.ExpectedCalls = nil
	h.kycs.ExpectedCalls = nil
	// Only the provider group is read here: the sweep resolves a specific
	// provider off each order, so it never walks the rail order.
	providerGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(providerGroup, ramp.SettingEnabled).Return(true, nil).Once()
	h.settings.EXPECT().String(providerGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Once()
	h.integ.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(
		&models.CustomerProviderIntegration{
			CustomerID: reconcileUser, Provider: models.ProviderBridge,
			Status: models.IntegrationStatusActive, ExternalCustomerID: support.Ptr("cus_1"),
			Metadata: "{}",
		}, nil).Once()
	approved := &models.CustomerProviderKyc{
		CustomerID: reconcileUser, Provider: models.ProviderBridge, KycStatus: models.KycStatusApproved,
	}
	require.NoError(t, approved.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
	}))
	h.kycs.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(approved, nil).Once()

	orders := []models.Transaction{
		staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_1"),
		staleOrder(2, models.TxStatusAwaitingFunds, later(), "trf_2"),
		staleOrder(3, models.TxStatusAwaitingFunds, later(), "trf_3"),
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return(orders, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(&ramp.RampOrder{Status: models.TxStatusAwaitingFunds}, nil).Times(3)

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 3, report.Scanned)
}

// A deposit is not a transfer. GetOrder reads `/transfers/{id}` and a deposit id
// is not one, so the sweep has to re-read it out of the account's history or it
// would 404 every standing-account order it ever scanned.
func TestSweepReconcilesADepositThroughTheAccountHistory(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")
	occurred := time.Now()

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()
	// The account row holds the provider-side account id; the customer reference
	// comes off the integration row, not the router — see depositAdapter.
	h.standing.EXPECT().GetDeposit(
		testifymock.Anything,
		testifymock.MatchedBy(func(ref ramp.CustomerRef) bool {
			return ref.CustomerID == reconcileUser && ref.ExternalID == "cus_1"
		}),
		"va_1", "dep_1",
	).Return(&ramp.VirtualAccountDeposit{
		DepositID: "dep_1", ProviderAccountID: "va_1",
		Status: models.TxStatusCompleted, SourceAmount: "100.0",
		DestinationAmount: "17.98", FeeAmount: "1.50", ExchangeFeeAmount: "0.20",
		TxHash: "0xabc", OccurredAt: &occurred,
	}, nil).Once()

	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusFundsReceived).
		Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, models.TxStatusCompleted, saved.Status)
	require.Equal(t, "100.0", saved.AmountFiat)
	require.Equal(t, "17.98", saved.AmountToken)
	require.Equal(t, "1.50", *saved.FeeAmount)
	require.Equal(t, "brl", *saved.FeeCurrency, "the fiat currency comes from the account row, never from the deposit")
	require.Equal(t, "0.20", *saved.ExchangeFeeAmount)
	require.Equal(t, "0xabc", *saved.DepositTxSignature)
	require.NotNil(t, saved.CompletedAt)
	h.adapter.AssertNotCalled(t, "GetOrder",
		testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The branch is on the row, not on the provider: Bridge serves both shapes, and
// an ordinary transfer must keep going through GetOrder.
func TestSweepReconcilesATransferThroughGetOrder(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	h.standing.AssertNotCalled(t, "GetDeposit",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.accounts.AssertNotCalled(t, "FindByID", testifymock.Anything)
}

// A company's order is re-read at the company's own provider customer: the
// sweep routes by the order's customer, never by the member who placed it.
func TestSweepAsksAboutACompanysOrderAtTheCompanysCustomer(t *testing.T) {
	const company = uint(1_000_000_000_007)
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_company")
	order.CustomerID = company
	h.integ.EXPECT().FindByCustomerAndProvider(company, models.ProviderBridge).Return(&models.CustomerProviderIntegration{
		CustomerID: company, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
		ExternalCustomerID: support.Ptr("cus_company"), Metadata: "{}",
	}, nil).Once()
	approved := &models.CustomerProviderKyc{CustomerID: company, Provider: models.ProviderBridge, KycStatus: models.KycStatusApproved}
	require.NoError(t, approved.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
	}))
	h.kycs.EXPECT().FindByCustomerAndProvider(company, models.ProviderBridge).Return(approved, nil).Once()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything,
		ramp.CustomerRef{CustomerID: company, ExternalID: "cus_company"}, "trf_company").
		Return(&ramp.RampOrder{Status: models.TxStatusAwaitingFunds}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)

	require.NoError(t, err)
	require.Zero(t, report.Failed)
}

// A deposit has no payment window: it arrives already funded, so `expires_at`
// stays nil and expire() — which keys on ExpiresAt != nil — leaves it alone.
// That is correct today only because of how expire() is written, and this test
// is what stops a refactor from breaking it silently.
func TestSweepNeverExpiresADeposit(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")
	require.Nil(t, order.ExpiresAt, "a deposit is created without a deposit window")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()
	h.standing.EXPECT().GetDeposit(
		testifymock.Anything, testifymock.Anything, "va_1", "dep_1",
	).Return(&ramp.VirtualAccountDeposit{
		DepositID: "dep_1", ProviderAccountID: "va_1", Status: models.TxStatusFundsReceived,
	}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired, "money already received cannot miss a deadline")
	require.Equal(t, 0, report.Updated, "an unchanged status writes nothing")
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A provider that does not serve standing accounts cannot answer for a deposit.
// The order is left alone and the sweep moves on, which is what every other
// refresh failure does.
func TestSweepFailsADepositWhoseProviderHasNoStandingAccounts(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")

	// A factory over the fat adapter alone: it serves the rail, but not this way.
	factory := ramp.NewFactory(h.adapter)
	router := ramp.NewRouter(ramp.RouterDeps{Factory: factory, Config: ramp.NewConfig(h.settings),
		Integrations: h.integ, Kyc: h.kycs, Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: mocks.NewMockCustomerIndividualProfileRepository(t)}, AppEnv: "local"})
	reconciler := rampsvc.NewReconciler(rampsvc.ReconcilerDeps{
		Router: router, Factory: factory, Integrations: h.integ, Transactions: h.txRepo,
		Accounts: h.accounts, Audience: broadcast.NewAudience(h.broadcaster, support.IndividualMembers(t)),
		Notifier: h.notifier, Config: ramp.NewConfig(h.settings),
		Deliverer: h.deliverer,
		Addresses: mocks.NewMockUserLiquidationAddressRepository(t),
	})

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()

	report, err := reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	require.Equal(t, 0, report.Updated)
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
}

// The deposit branch uses the same sweep-scoped cache as transfers: N deposits
// into one standing account must not re-run FindByID and the integration lookup
// per order.
func TestSweepCachesTheAccountAndAdapterAcrossDeposits(t *testing.T) {
	h := newReconcilerHarness(t)
	h.integ.ExpectedCalls = nil
	h.integ.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(
		&models.CustomerProviderIntegration{
			CustomerID: reconcileUser, Provider: models.ProviderBridge,
			Status: models.IntegrationStatusActive, ExternalCustomerID: support.Ptr("cus_1"),
			Metadata: "{}",
		}, nil).Once()

	first := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")
	second := staleDeposit(2, models.TxStatusFundsReceived, "dep_2")
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{first, second}, nil).Once()
	h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()
	h.standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, "va_1", "dep_1").
		Return(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1", Status: models.TxStatusFundsReceived,
		}, nil).Once()
	h.standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, "va_1", "dep_2").
		Return(&ramp.VirtualAccountDeposit{
			DepositID: "dep_2", ProviderAccountID: "va_1", Status: models.TxStatusFundsReceived,
		}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 2, report.Scanned)
	require.Equal(t, 0, report.Failed)
}

// A deposit still reconciles after its owner's endorsement lapses.
//
// The sweep is the safety net for a webhook that never arrived, so the orders it
// must resolve are money the provider already took. Routing the deposit branch
// would gate it on "may this user transact", freezing exactly those orders — and
// the webhook path is not gated, so the two would disagree about the same row.
//
// The KYC row here clears no rail, which is what the router refuses on.
func TestSweepReconcilesADepositWithoutTheEligibilityChain(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")

	lapsed := &models.CustomerProviderKyc{
		CustomerID: reconcileUser, Provider: models.ProviderBridge, KycStatus: models.KycStatusPaused,
	}
	require.NoError(t, lapsed.SetEndorsements(nil))
	h.kycs.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(lapsed, nil).Maybe()

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()
	h.standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, "va_1", "dep_1").
		Return(&ramp.VirtualAccountDeposit{
			DepositID: "dep_1", ProviderAccountID: "va_1",
			Status: models.TxStatusCompleted, SourceAmount: "100.0", DestinationAmount: "17.98",
		}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusFundsReceived).
		Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated, "money already taken must still resolve")
	require.Equal(t, 0, report.Failed)
}

func TestReconciler_RefreshOne_PublishesOnChange(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_1")

	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted, TxHash: "0xabc"}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.True(t, changed)
}

// The admin's refresh is one more read that can see an on-ramp complete first,
// and it has no sweep behind it: it asks for the delivery itself.
func TestReconciler_RefreshOne_DeliversAnOnrampItCompleted(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusFundsReceived, later(), "trf_1")

	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, h.deliverer.delivered, 1)
	require.Equal(t, order.ExternalID, h.deliverer.delivered[0].ExternalID)
}

// A refresh that leaves the order in flight has nothing in custody to forward.
func TestReconciler_RefreshOne_DoesNotDeliverAnOnrampStillInFlight(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusAwaitingFunds, later(), "trf_1")

	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusFundsReceived}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	_, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.Empty(t, h.deliverer.delivered)
}

func TestReconciler_RefreshOne_NoPublishWhenUnchanged(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusFundsReceived, later(), "trf_1")

	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusFundsReceived}, nil).Once()

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.False(t, changed)
	h.txRepo.AssertNotCalled(t, "ExpireUnfunded", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.announced)
}

// --- undelivered on-ramps ---------------------------------------------------

// The webhook is the fast path for the second hop, and it gets one attempt. A
// send that failed there — the provider throttled, the corridor briefly shut —
// would otherwise sit forever: the order is already terminal, so no later event
// reopens it, and nothing else looks at it. This sweep is that safety net.
func TestSweepDeliversASettledOnrampThatTheWebhookMissed(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return(nil, nil).Once()

	settled := models.Transaction{
		Model: orm.Model{ID: 5}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_undelivered",
		Status: models.TxStatusCompleted, AmountToken: "88.200000", TargetToken: "USDC",
		DestinationWallet: "0x742d", Chain: support.Ptr("polygon"),
	}
	h.txRepo.EXPECT().FindDeliveriesInFlight(testifymock.Anything, testifymock.Anything, 20).Return(nil, nil).Once()
	h.txRepo.EXPECT().FindUndeliveredOnramps(testifymock.Anything, 3, 20).
		Return([]models.Transaction{settled}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Delivered)
	require.Len(t, h.deliverer.delivered, 1)
	require.Equal(t, "ext_undelivered", h.deliverer.delivered[0].ExternalID)
}

// A delivery whose own webhook was lost is on a completed row the stale-order
// query never returns. The sweep re-reads it, records the failure the way the
// webhook would, and retries it in the same run.
func TestSweepRecordsALostDeliveryFailureAndRetriesIt(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return(nil, nil).Once()

	inFlight := models.Transaction{
		Model: orm.Model{ID: 5}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_lost", GatewayOrderID: "tr_payin",
		Status: models.TxStatusProcessing, AmountFiat: "500.00", AmountToken: "88.200000",
		TargetToken: "USDC", DestinationWallet: "0x742d", Chain: support.Ptr("polygon"),
		DeliveryOrderID: support.Ptr("tr_delivery"),
	}
	h.txRepo.EXPECT().FindDeliveriesInFlight(testifymock.Anything, testifymock.Anything, 20).
		Return([]models.Transaction{inFlight}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "tr_delivery").Return(&ramp.RampOrder{
		ProviderOrderID: "tr_delivery", Status: models.TxStatusProcessing,
		DeliveryFailed: true, FailureReason: "boom",
		Amounts: map[string]string{"source_amount": "88.2"},
	}, nil).Once()
	h.txRepo.EXPECT().SaveDeliveryOutcome(testifymock.MatchedBy(func(tx *models.Transaction) bool {
		return tx.DeliveryOrderID == nil && tx.DeliveryAttempts == 1 &&
			tx.GatewayOrderID == "tr_payin" && tx.AmountFiat == "500.00" &&
			tx.FailureReason != nil
	}), "tr_delivery").Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	retry := inFlight
	retry.DeliveryOrderID = nil
	retry.DeliveryAttempts = 1
	h.txRepo.EXPECT().FindUndeliveredOnramps(testifymock.Anything, 3, 20).
		Return([]models.Transaction{retry}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 1, report.Delivered)
	require.Len(t, h.deliverer.delivered, 1)
}

// A webhook that recorded the same outcome first wins the conditional write:
// the sweep reports no change and tells nobody, so the outcome counts once.
func TestSweepLosesTheDeliveryOutcomeRaceQuietly(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return(nil, nil).Once()
	inFlight := models.Transaction{
		Model: orm.Model{ID: 5}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_raced", GatewayOrderID: "tr_payin",
		Status: models.TxStatusProcessing, TargetToken: "USDC", DestinationWallet: "0x742d",
		DeliveryOrderID: support.Ptr("tr_delivery"),
	}
	h.txRepo.EXPECT().FindDeliveriesInFlight(testifymock.Anything, testifymock.Anything, 20).
		Return([]models.Transaction{inFlight}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "tr_delivery").Return(&ramp.RampOrder{
		ProviderOrderID: "tr_delivery", Status: models.TxStatusProcessing, DeliveryFailed: true,
	}, nil).Once()
	h.txRepo.EXPECT().SaveDeliveryOutcome(testifymock.Anything, "tr_delivery").Return(false, nil).Once()
	h.txRepo.EXPECT().FindUndeliveredOnramps(testifymock.Anything, 3, 20).Return(nil, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 0, report.Failed)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// One failed delivery must not abort the batch, and it must be reported rather
// than swallowed — the money is sitting in the provider's custody and somebody
// has to know.
func TestSweepReportsADeliveryThatFailsAndKeepsGoing(t *testing.T) {
	h := newDeliveringReconcilerHarness(t)
	h.deliverer.err = errors.New("provider refused the send")
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return(nil, nil).Once()

	settled := models.Transaction{
		Model: orm.Model{ID: 5}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOnramp, ExternalID: "ext_undelivered",
		Status: models.TxStatusCompleted, AmountToken: "88.200000", TargetToken: "USDC",
		DestinationWallet: "0x742d", Chain: support.Ptr("polygon"),
	}
	h.txRepo.EXPECT().FindDeliveriesInFlight(testifymock.Anything, testifymock.Anything, 20).Return(nil, nil).Once()
	h.txRepo.EXPECT().FindUndeliveredOnramps(testifymock.Anything, 3, 20).
		Return([]models.Transaction{settled, settled}, nil).Once()

	// A failed delivery still touches the row. The query is ordered oldest-first
	// on updated_at, so a row that fails deterministically and is never written
	// would sit at the head of every future sweep and starve every on-ramp
	// settled after it — the safety net silently stopping for everyone once
	// `limit` orders are stuck.
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Twice()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Delivered)
	require.Equal(t, 2, report.Failed)
	require.Len(t, h.deliverer.delivered, 2, "one failure must not abort the batch")
}

// Nothing is saved for a deposit the history cannot move forward.
func TestSweepLeavesADepositAlone(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		deposit *ramp.VirtualAccountDeposit
	}{
		{name: "the provider has no record of it", status: models.TxStatusFundsReceived},
		{name: "it is settled and the history lags", status: models.TxStatusCompleted,
			deposit: &ramp.VirtualAccountDeposit{DepositID: "dep_1", ProviderAccountID: "va_1", Status: models.TxStatusFundsReceived, SourceAmount: "100.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newReconcilerHarness(t)
			h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
				Return([]models.Transaction{staleDeposit(1, c.status, "dep_1")}, nil).Once()
			h.accounts.EXPECT().FindByID(uint(3)).Return(virtualAccountRow(), nil).Once()
			h.standing.EXPECT().GetDeposit(testifymock.Anything, testifymock.Anything, "va_1", "dep_1").Return(c.deposit, nil).Once()

			report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)

			require.NoError(t, err)
			require.Equal(t, 0, report.Updated)
			require.Equal(t, 0, report.Failed)
		})
	}
}

// An operator has to see a deposit nothing can be asked about, not have the
// sweep pass over it in silence every run.
func TestSweepFailsADepositItCannotAskAbout(t *testing.T) {
	cases := []struct {
		name    string
		account *models.CustomerVirtualAccount
	}{
		{name: "its account is gone"},
		{name: "its customer has no provider customer", account: virtualAccountRow()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newReconcilerHarness(t)
			order := staleDeposit(1, models.TxStatusFundsReceived, "dep_1")
			order.CustomerID = 8
			h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
			h.accounts.EXPECT().FindByID(uint(3)).Return(c.account, nil).Once()
			if c.account != nil {
				h.integ.EXPECT().FindByCustomerAndProvider(uint(8), models.ProviderBridge).Return(nil, nil).Once()
			}

			report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)

			require.NoError(t, err)
			require.Equal(t, 1, report.Failed)
		})
	}
}

// awaitingOzavOfframp is an off-ramp whose funding window has closed and that
// no provider transfer pays out yet.
func awaitingOzavOfframp() models.Transaction {
	past := time.Now().Add(-time.Minute)
	return models.Transaction{
		Model: orm.Model{ID: 9}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderOzav,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_offramp",
		Status: models.TxStatusAwaitingFunds, AmountToken: "17", TargetToken: "BRS",
		ExpiresAt: &past,
	}
}

// A deposit seen only through the balance has no webhook hash to protect the
// order, so funding is checked before the window closes it.
func TestSweepSettlesAFundedOfframpInsteadOfExpiringIt(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).Return(true, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 0, report.Failed)
}

// Funding that cannot be read is not funding that is absent: the order is left
// for the next sweep rather than expired on a guess.
func TestSweepLeavesAnOfframpWhoseFundingCannotBeRead(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).
		Return(false, errors.New("ozav: GET /balances: 503")).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 1, report.Failed)
}

func TestSweepExpiresAnUnfundedOfframp(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).Return(false, nil).Once()
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(true, nil)).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Expired)
}

// The row was read before the balance check's provider call, and a deposit
// claimed in between keeps it open: the conditional write loses, and nobody is
// told the order expired.
func TestSweepDoesNotAnnounceAnExpiryAConcurrentClaimPrevented(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).Return(false, nil).Once()
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).RunAndReturn(expireWrites(false, nil)).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Expired)
	require.Equal(t, 0, report.Failed)
	require.Empty(t, h.announced)
	h.txRepo.AssertNotCalled(t, "Update", testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestSweepCountsAnExpiryItCouldNotWrite(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, testifymock.Anything).Return(false, nil).Once()
	h.txRepo.EXPECT().ExpireUnfunded(testifymock.Anything).Return(false, errors.New("db down")).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	require.Empty(t, h.announced)
}

// expireWrites answers ExpireUnfunded the way the repository does: when it
// wrote, the row it was handed now reads expired. saved, when set, receives it.
func expireWrites(wrote bool, saved **models.Transaction) func(*models.Transaction) (bool, error) {
	return func(tx *models.Transaction) (bool, error) {
		if saved != nil {
			*saved = tx
		}
		if wrote {
			tx.Status = models.TxStatusExpired
		}
		return wrote, nil
	}
}

func TestRefreshOneSettlesAFundedOfframp(t *testing.T) {
	h := newReconcilerHarness(t)
	order := awaitingOzavOfframp()
	h.settler.EXPECT().EnqueueSettleIfFunded(testifymock.Anything, &order).Return(true, nil).Once()

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.False(t, changed)
}

// Review Focus 4 on the refresh path: a read that still says processing does
// not pull a payout under refund review back, but what else it says is kept.
func TestSweepKeepsAPayoutUnderRefundReviewOutOfProcessing(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusRefundAttention, later(), "po_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "po_1").
		Return(&ramp.RampOrder{Status: models.TxStatusProcessing, TxHash: "0xabc", Raw: []byte(`{"id":"po_1"}`)}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusRefundAttention).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Maybe()

	_, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusRefundAttention, saved.Status)
	require.Equal(t, "0xabc", *saved.DepositTxSignature, "the fresh read is still information")
	require.Equal(t, `{"id":"po_1"}`, saved.RawPayload)
}

// A read that says failed never hides held funds behind a terminal status.
func TestSweepKeepsAPayoutUnderRefundReviewOutOfFailed(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusRefundAttention, later(), "po_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "po_1").
		Return(&ramp.RampOrder{Status: models.TxStatusFailed, TxHash: "0xabc", Raw: []byte(`{"id":"po_1"}`)}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusRefundAttention).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Maybe()

	_, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, models.TxStatusRefundAttention, saved.Status)
	require.Equal(t, `{"id":"po_1"}`, saved.RawPayload)
}

func TestSweepWritesNothingWhenOnlyTheRegressingStatusDiffers(t *testing.T) {
	h := newReconcilerHarness(t)
	order := staleOrder(1, models.TxStatusReturning, later(), "po_1")

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{order}, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "po_1").
		Return(&ramp.RampOrder{Status: models.TxStatusProcessing}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}
