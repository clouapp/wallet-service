package ramp_test

import (
	"context"
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

// payoutProvider is Bridge with the liquidation-address port.
type payoutProvider struct {
	*mocks.MockProviderAdapter
	*mocks.MockLiquidationAddressAdapter
}

func (p *payoutProvider) Code() ramp.ProviderCode { return p.MockProviderAdapter.Code() }

// staleDrain is a USD payout a drain opened: no expiry, the drain id in
// GatewayOrderID, and the address that names where to ask.
func staleDrain(status string) models.Transaction {
	rail, addressID := models.RailUSDACHPayout, uint(5)
	return models.Transaction{
		Model: orm.Model{ID: 1}, CustomerID: reconcileUser, UserID: reconcileUser, Provider: models.ProviderBridge,
		Type: models.TransactionTypeOfframp, ExternalID: "ext_drain", GatewayOrderID: "drn_1",
		DepositID: support.Ptr("drn_1"), Status: status, Rail: &rail, LiquidationAddressID: &addressID,
		FiatCurrency: models.FiatCurrencyUSD, AmountFiat: "0", AmountToken: "250.00", TargetToken: "USDC",
	}
}

type drainSweep struct {
	reconciler  *rampsvc.Reconciler
	payout      *payoutProvider
	txRepo      *mocks.MockTransactionRepository
	addresses   *mocks.MockUserLiquidationAddressRepository
	broadcaster *mocks.MockBroadcaster
}

// newDrainSweep wires a reconciler with no router: a drain is re-read off the
// factory and the integration, never routed.
func newDrainSweep(t *testing.T) *drainSweep {
	t.Helper()
	newHarness(t) // binds the Log facade the sweep writes to
	s := &drainSweep{
		payout: &payoutProvider{
			MockProviderAdapter:           mocks.NewMockProviderAdapter(t),
			MockLiquidationAddressAdapter: mocks.NewMockLiquidationAddressAdapter(t),
		},
		txRepo:      mocks.NewMockTransactionRepository(t),
		addresses:   mocks.NewMockUserLiquidationAddressRepository(t),
		broadcaster: mocks.NewMockBroadcaster(t),
	}
	s.payout.MockProviderAdapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	integ.EXPECT().FindByCustomerAndProvider(reconcileUser, models.ProviderBridge).Return(&models.CustomerProviderIntegration{
		CustomerID: reconcileUser, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
		ExternalCustomerID: support.Ptr("cus_1"), Metadata: "{}",
	}, nil).Maybe()
	notifier := mocks.NewMockNotifier(t)
	notifier.EXPECT().TransactionUpdated(testifymock.Anything).Maybe()
	s.reconciler = rampsvc.NewReconciler(rampsvc.ReconcilerDeps{
		Factory: ramp.NewFactory(s.payout), Integrations: integ, Transactions: s.txRepo,
		Accounts: mocks.NewMockCustomerVirtualAccountRepository(t), Audience: broadcast.NewAudience(s.broadcaster, support.IndividualMembers(t)),
		Notifier: notifier, Deliverer: &recordingDeliverer{}, Addresses: s.addresses,
	})
	return s
}

func TestSweepReconcilesADrainThroughTheAddressHistory(t *testing.T) {
	s := newDrainSweep(t)
	settled := time.Now()
	s.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{staleDrain(models.TxStatusFundsReceived)}, nil).Once()
	s.addresses.EXPECT().FindByID(uint(5)).Return(liquidationAddressRow(), nil).Once()
	s.payout.MockLiquidationAddressAdapter.EXPECT().GetDrain(testifymock.Anything,
		ramp.CustomerRef{CustomerID: reconcileUser, ExternalID: "cus_1"}, "la_1", "drn_1").
		Return(&ramp.LiquidationDrain{
			DrainID: "drn_1", Status: models.TxStatusCompleted,
			DestinationAmount: "247.50", FeeAmount: "2.50", OccurredAt: &settled,
		}, nil).Once()
	var saved *models.Transaction
	s.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusFundsReceived).
		Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	s.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := s.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, models.TxStatusCompleted, saved.Status)
	require.Equal(t, "247.50", saved.AmountFiat)
	require.Equal(t, "2.50", *saved.FeeAmount)
	require.NotNil(t, saved.CompletedAt)
}

// An order pointing at an address that is gone is an anomaly the operator has
// to see, not something the sweep skips in silence.
func TestSweepFailsADrainWhoseAddressIsGone(t *testing.T) {
	s := newDrainSweep(t)
	s.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).
		Return([]models.Transaction{staleDrain(models.TxStatusFundsReceived)}, nil).Once()
	s.addresses.EXPECT().FindByID(uint(5)).Return(nil, nil).Once()

	report, err := s.reconciler.Sweep(context.Background(), time.Minute, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	require.Equal(t, 0, report.Updated)
}
