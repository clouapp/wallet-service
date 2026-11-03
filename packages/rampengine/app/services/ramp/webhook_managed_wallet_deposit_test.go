package ramp_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// walletOwnerAdapter is an Ozav adapter that can name a managed wallet's
// customer, recording when it was asked.
type walletOwnerAdapter struct {
	*mocks.MockProviderAdapter
	customerID string
	err        error
	calls      *[]string
}

func (a *walletOwnerAdapter) CustomerIDForWallet(context.Context, string) (string, error) {
	*a.calls = append(*a.calls, "resolve owner")
	return a.customerID, a.err
}

type depositHarness struct {
	*harness
	settler *mocks.MockOfframpSettler
	owner   *walletOwnerAdapter
	calls   []string
}

func newDepositHarness(t *testing.T) *depositHarness {
	t.Helper()
	h := &depositHarness{harness: newHarness(t), settler: mocks.NewMockOfframpSettler(t)}
	ozav := mocks.NewMockProviderAdapter(t)
	ozav.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	ozav.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()
	h.owner = &walletOwnerAdapter{MockProviderAdapter: ozav, customerID: "ozav_cus_1", calls: &h.calls}
	h.processor = rampsvc.NewWebhookProcessor(rampsvc.WebhookProcessorDeps{
		Factory: ramp.NewFactory(h.owner), Config: ramp.NewConfig(nil), Events: h.events,
		Integrations: h.integRepo, Accounts: h.accounts, Members: h.members, Broadcaster: h.broadcaster,
		Notifier: h.notifier, Features: openFeatures(), Deliverer: h.deliverer, Settler: h.settler,
	})
	ozav.EXPECT().DecodeWebhook(testifymock.Anything, testifymock.Anything).Return(&ramp.WebhookResult{
		Kind: ramp.WebhookKindManagedWalletDeposit, EventID: "evt_dep", EventType: "deposit.credited",
		ManagedWalletDeposit: &ramp.ManagedWalletDeposit{
			WalletID: "ozav_wal_1", Amount: "17", Currency: "BRS", Network: "solana", TxHash: "5sig",
		},
	}, nil).Once()
	return h
}

func managedWalletEventRow() *models.WebhookEvent {
	row := eventRow()
	row.Provider = models.ProviderOzav
	return row
}

// lockedRecording answers WithLocked for row, noting when the transaction
// opens and commits.
func (h *depositHarness) lockedRecording(row *models.WebhookEvent) {
	h.events.EXPECT().WithLocked(row.ID, testifymock.Anything).RunAndReturn(
		func(_ uint, fn func(*models.WebhookEvent, repositories.WebhookTx) error) error {
			h.calls = append(h.calls, "lock")
			if err := fn(row, h.db); err != nil {
				return err
			}
			h.calls = append(h.calls, "commit")
			return nil
		})
}

// The owner is a provider call, so it is asked before the event transaction
// opens; the settle enqueue is money moving, so it runs only after the commit.
func TestProcessResolvesTheDepositOwnerBeforeTheLockAndEnqueuesAfterTheCommit(t *testing.T) {
	h := newDepositHarness(t)
	row := managedWalletEventRow()
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.lockedRecording(row)
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderOzav, "ozav_cus_1").
		Return(&models.CustomerProviderIntegration{CustomerID: 7, ExternalCustomerID: support.Ptr("ozav_cus_1")}, nil).Once()
	h.settler.EXPECT().ApplyManagedWalletDeposit(testifymock.Anything, uint(7), testifymock.Anything).
		Return(func() { h.calls = append(h.calls, "enqueue") }, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Equal(t, []string{"resolve owner", "lock", "commit", "enqueue"}, h.calls)
}

// A deposit into a wallet whose customer we do not know is recorded and left.
func TestProcessIgnoresADepositForAnUnknownCustomer(t *testing.T) {
	h := newDepositHarness(t)
	row := managedWalletEventRow()
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.lockedRecording(row)
	h.integRepo.EXPECT().FindByExternalCustomerID(models.ProviderOzav, "ozav_cus_1").Return(nil, nil).Once()
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.NoError(t, h.processor.Process(context.Background(), eventRowID))
	require.Nil(t, row.ProcessingError)
}

// An owner the provider cannot name fails the event before any transaction
// opens for it, and nothing is claimed.
func TestProcessFailsADepositWhoseOwnerCannotBeResolved(t *testing.T) {
	h := newDepositHarness(t)
	h.owner.err = errors.New("ozav: GET /wallets/ozav_wal_1: 503")
	row := managedWalletEventRow()
	h.events.EXPECT().FindByID(row.ID).Return(row, nil).Once()
	h.lockedRecording(row)
	h.db.EXPECT().UpdateEvent(row).Return(nil).Once()

	require.Error(t, h.processor.Process(context.Background(), eventRowID))
	require.NotNil(t, row.ProcessingError)
	require.Equal(t, []string{"resolve owner", "lock", "commit"}, h.calls, "only the failure stamp opens a transaction")
}
