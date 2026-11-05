package ramp_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/mutualpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// syncHarness wires the standing-account syncer over mocks.
//
// There is no router in it, and that is the design under test: the sweep
// resolves the adapter from the factory and the customer id off the integration
// row. See the note on VirtualAccountSyncer.
type syncHarness struct {
	syncer   *rampsvc.VirtualAccountSyncer
	adapter  *mocks.MockProviderAdapter
	standing *mocks.MockVirtualAccountAdapter
	integ    *mocks.MockCustomerProviderIntegrationRepository
	accounts *mocks.MockCustomerVirtualAccountRepository
	settings *mocks.MockSettingsSource
}

const syncUser = uint(11)

func newSyncHarness(t *testing.T, railFee string) *syncHarness {
	t.Helper()
	newHarness(t) // binds the Log facade the sweep writes to

	h := &syncHarness{
		adapter:  mocks.NewMockProviderAdapter(t),
		standing: mocks.NewMockVirtualAccountAdapter(t),
		integ:    mocks.NewMockCustomerProviderIntegrationRepository(t),
		accounts: mocks.NewMockCustomerVirtualAccountRepository(t),
		settings: mocks.NewMockSettingsSource(t),
	}
	h.adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	railGroup := ramp.RailGroup(ramp.RailPixPayin)
	h.settings.EXPECT().String(railGroup, ramp.SettingProviderOrder).
		Return(models.ProviderBridge, nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return(railFee, nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.FeeSettingKey(ramp.ProviderBlindPay)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingMaxAmountBRL).Return("", nil).Maybe()
	// The USD deposit rail is born closed: an empty order, so the sweep
	// leaves USD accounts alone unless a test opens it (openUSD).
	usdGroup := ramp.RailGroup(ramp.RailUSDPayin)
	h.settings.EXPECT().String(usdGroup, ramp.SettingProviderOrder).Return("", nil).Maybe()
	h.settings.EXPECT().String(usdGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(usdGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
	h.settings.EXPECT().String(railGroup, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()

	provider := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	h.syncer = rampsvc.NewVirtualAccountSyncer(ramp.NewFactory(provider), h.integ, h.accounts, ramp.NewConfig(h.settings))
	return h
}

// openUSD reconfigures the rails: pix_payin keeps pixFee, and usd_payin opens
// on bridge with usdFee. The expectations are rebuilt because a mock answers
// with the first one that matches.
func (h *syncHarness) openUSD(pixFee, usdFee string) {
	h.settings.ExpectedCalls = nil
	pixGroup := ramp.RailGroup(ramp.RailPixPayin)
	h.settings.EXPECT().String(pixGroup, ramp.SettingProviderOrder).Return(models.ProviderBridge, nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return(pixFee, nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.FeeSettingKey(ramp.ProviderBlindPay)).Return("0.00", nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.SettingMinAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.SettingMinAmountBRLByToken).Return("", nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.SettingMaxAmountBRL).Return("", nil).Maybe()
	h.settings.EXPECT().String(pixGroup, ramp.SettingOzavDeliveryMaxAttempts).Return("3", nil).Maybe()
	usdGroup := ramp.RailGroup(ramp.RailUSDPayin)
	h.settings.EXPECT().String(usdGroup, ramp.SettingProviderOrder).Return(models.ProviderBridge, nil).Maybe()
	h.settings.EXPECT().String(usdGroup, ramp.FeeSettingKey(ramp.ProviderBridge)).Return(usdFee, nil).Maybe()
	h.settings.EXPECT().String(usdGroup, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
}

// linked gives the user a provider customer to be asked about.
func (h *syncHarness) linked(status string) {
	h.integ.EXPECT().FindByCustomerIDsAndProvider(testifymock.Anything, models.ProviderBridge).Return(
		[]models.CustomerProviderIntegration{{
			CustomerID: syncUser, Provider: models.ProviderBridge, Status: status,
			ExternalCustomerID: support.Ptr("cus_1"), Metadata: "{}",
		}}, nil).Maybe()
}

// activeAccounts is what the sweep walks.
func (h *syncHarness) activeAccounts(rows ...models.CustomerVirtualAccount) {
	h.accounts.EXPECT().FindActiveByProvider(models.ProviderBridge, testifymock.Anything).Return(rows, nil).Once()
}

// standingRow is a provisioned account as XIP has it stored.
func standingRow(id uint, feePercent string) models.CustomerVirtualAccount {
	fee := feePercent
	return models.CustomerVirtualAccount{
		Model: orm.Model{ID: id}, CustomerID: syncUser, UserID: syncUser, Provider: models.ProviderBridge,
		ExternalAccountID:   fmt.Sprintf("va_%d", id),
		SourceCurrency:      "brl",
		Status:              models.VirtualAccountStatusActive,
		DestinationCurrency: "usdc",
		DestinationChain:    "solana",
		DestinationAddress:  "8nWZBsolanawallet",
		FeePercent:          &fee,
		DepositInstructions: `{"br_code":"00020126STUB"}`,
		RawPayload:          "{}",
	}
}

// providerView is the same account as the provider answers it.
func providerView(row models.CustomerVirtualAccount) *ramp.VirtualAccount {
	fee := ""
	if row.FeePercent != nil {
		fee = *row.FeePercent
	}
	return &ramp.VirtualAccount{
		ProviderAccountID:   row.ExternalAccountID,
		Status:              row.Status,
		DepositInstructions: map[string]any{"br_code": "00020126STUB"},
		Destination: ramp.CryptoLeg{
			Currency:    row.DestinationCurrency,
			PaymentRail: row.DestinationChain,
			Address:     row.DestinationAddress,
		},
		SourceCurrency: row.SourceCurrency,
		FeePercent:     fee,
	}
}

// The contract `ramps:reconcile` carries: a sweep that finds nothing changed
// writes nothing. An account matching the provider and the rail on every field
// costs one read and no more.
func TestSyncVirtualAccounts_UnchangedWritesNothing(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(providerView(row), nil).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Scanned)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 0, report.FeePushed)
	require.Equal(t, 0, report.Failed)
}

// "1.5" and "1.50" are the same fee. Comparing them as strings would push a PUT
// on every run — the opposite of idempotent, and a write to the provider for no
// reason at all.
func TestSyncVirtualAccounts_EquivalentFeesAreEqual(t *testing.T) {
	h := newSyncHarness(t, "1.5")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(providerView(row), nil).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, report.FeePushed)
	require.Equal(t, 0, report.Updated)
}

// The provider is the truth about status. An account deactivated upstream must
// stop being handed out as "your deposit account": funds sent to it are returned
// to the sender.
func TestSyncVirtualAccounts_PersistsProviderStatus(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)

	current := providerView(row)
	current.Status = models.VirtualAccountStatusInactive
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(current, nil).Once()

	var saved *models.CustomerVirtualAccount
	h.accounts.EXPECT().Update(testifymock.Anything).RunAndReturn(
		func(account *models.CustomerVirtualAccount) error { saved = account; return nil }).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.NotNil(t, saved)
	require.Equal(t, models.VirtualAccountStatusInactive, saved.Status)
	require.NotNil(t, saved.LastSyncedAt, "the sweep stamps what it compared")
	require.JSONEq(t, `{"br_code":"00020126STUB"}`, saved.DepositInstructions,
		"the instructions are persisted as the provider returned them, never rotated")
}

// The destination is where the money ends up. A change made upstream has to
// land on the row, or the admin panel shows a wallet that is no longer paid.
func TestSyncVirtualAccounts_PersistsProviderDestination(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ramp.CryptoLeg)
		stored func(*models.CustomerVirtualAccount) string
		want   string
	}{
		{"address", func(d *ramp.CryptoLeg) { d.Address = "OtherSolanaWallet" },
			func(a *models.CustomerVirtualAccount) string { return a.DestinationAddress }, "OtherSolanaWallet"},
		{"currency", func(d *ramp.CryptoLeg) { d.Currency = "usdt" },
			func(a *models.CustomerVirtualAccount) string { return a.DestinationCurrency }, "usdt"},
		{"chain", func(d *ramp.CryptoLeg) { d.PaymentRail = "ethereum" },
			func(a *models.CustomerVirtualAccount) string { return a.DestinationChain }, "ethereum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSyncHarness(t, "1.50")
			h.linked(models.IntegrationStatusActive)
			row := standingRow(1, "1.50")
			h.activeAccounts(row)
			current := providerView(row)
			c.change(&current.Destination)
			h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
				Return(current, nil).Once()
			var saved *models.CustomerVirtualAccount
			h.accounts.EXPECT().Update(testifymock.Anything).RunAndReturn(
				func(account *models.CustomerVirtualAccount) error { saved = account; return nil }).Once()

			report, err := h.syncer.Sync(context.Background())

			require.NoError(t, err)
			require.Equal(t, 1, report.Updated)
			require.Equal(t, c.want, c.stored(saved))
		})
	}
}

func TestSyncVirtualAccounts_AnAccountTheProviderDoesNotReturnIsLeftAlone(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	h.activeAccounts(standingRow(1, "1.50"))
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").Return(nil, nil).Once()

	report, err := h.syncer.Sync(context.Background())

	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 0, report.Failed)
}

// With no payin rail open there is no fee to propagate anywhere: the sweep
// refuses instead of walking every account to conclude nothing.
func TestSyncVirtualAccounts_RefusesARailWithNoProviders(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.settings.ExpectedCalls = nil
	railGroup := ramp.RailGroup(ramp.RailPixPayin)
	h.settings.EXPECT().String(railGroup, testifymock.Anything).Return("", nil).Maybe()
	h.settings.EXPECT().String(ramp.RailGroup(ramp.RailUSDPayin), testifymock.Anything).Return("", nil).Maybe()

	_, err := h.syncer.Sync(context.Background())

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// The sweep pushes the fee of the account's own provider. The harness gives
// Ozav 9.99, so a sweep reading Ozav's key, or a single rail-wide fee, would
// push it onto a Bridge account that is already right.
func TestSyncVirtualAccounts_PushesOnlyTheAccountProvidersFee(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(providerView(row), nil).Once()

	report, err := h.syncer.Sync(context.Background())

	require.NoError(t, err)
	require.Equal(t, 0, report.FeePushed)
	h.standing.AssertNotCalled(t, "UpdateVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Q3 answered yes: the PUT accepts developer_fee_percent. So changing the fee in
// the admin panel propagates to accounts already provisioned. Without this the
// edit is a change that silently does not take effect.
func TestSyncVirtualAccounts_PushesTheRailFee(t *testing.T) {
	h := newSyncHarness(t, "2.00")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(providerView(row), nil).Once()

	updated := providerView(row)
	updated.FeePercent = "2.00"
	var sent ramp.VirtualAccountUpdate
	h.standing.EXPECT().UpdateVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1", testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, _ string, req ramp.VirtualAccountUpdate) (*ramp.VirtualAccount, error) {
			sent = req
			return updated, nil
		}).Once()

	var saved *models.CustomerVirtualAccount
	h.accounts.EXPECT().Update(testifymock.Anything).RunAndReturn(
		func(account *models.CustomerVirtualAccount) error { saved = account; return nil }).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.FeePushed)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, "2.00", sent.FeePercent)
	require.Nil(t, sent.Destination, "a fee change must not restate a destination it did not read")
	require.Nil(t, sent.Active, "a fee change must not touch the activation")
	require.NotNil(t, saved.FeePercent)
	require.Equal(t, "2.00", *saved.FeePercent)
}

// A deactivated account collects nothing, so there is no fee to propagate onto
// it. The row still learns it is inactive.
func TestSyncVirtualAccounts_SkipsTheFeePushOnAnInactiveAccount(t *testing.T) {
	h := newSyncHarness(t, "2.00")
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)

	current := providerView(row)
	current.Status = models.VirtualAccountStatusInactive
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(current, nil).Once()

	var saved *models.CustomerVirtualAccount
	h.accounts.EXPECT().Update(testifymock.Anything).RunAndReturn(
		func(account *models.CustomerVirtualAccount) error { saved = account; return nil }).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, report.FeePushed)
	require.Equal(t, models.VirtualAccountStatusInactive, saved.Status)
}

// The sweep is bookkeeping, not transacting. A user whose integration lapsed
// still has an account whose status and fee must stay true — and the router,
// which is what would refuse here, is deliberately not in the path.
func TestSyncVirtualAccounts_IgnoresEligibility(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusDisabled)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)

	current := providerView(row)
	current.Status = models.VirtualAccountStatusInactive
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(current, nil).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 0, report.Failed)
}

// One provider being down, or one row being broken, must not abort the batch —
// the same rule the reconcile sweep follows.
func TestSyncVirtualAccounts_KeepsGoingAfterAFailure(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	first, second := standingRow(1, "1.50"), standingRow(2, "1.50")
	h.activeAccounts(first, second)

	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(nil, errors.New("bridge is down")).Once()

	current := providerView(second)
	current.Status = models.VirtualAccountStatusInactive
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_2").
		Return(current, nil).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, report.Scanned)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 1, report.Failed)
}

// Without a customer id there is nobody to ask about the account. That is an
// anomaly an operator has to see, not a row the sweep passes over in silence.
func TestSyncVirtualAccounts_FailsWithoutACustomer(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.integ.EXPECT().FindByCustomerIDsAndProvider(testifymock.Anything, models.ProviderBridge).
		Return(nil, nil).Once()
	h.activeAccounts(standingRow(1, "1.50"))

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	require.Equal(t, 0, report.Updated)
}

// usdRow is a USD account: the same provisioned row in dollars.
func usdRow(id uint, feePercent string) models.CustomerVirtualAccount {
	row := standingRow(id, feePercent)
	row.SourceCurrency = "usd"
	row.DepositInstructions = `{"currency":"usd","bank_routing_number":"101019644"}`
	return row
}

// A USD account is held to usd_payin's fee, never pix_payin's: before the
// rails were per currency, the sweep pushed the Pix fee onto every account.
func TestSyncVirtualAccounts_PushesTheUSDRailFeeOntoAUSDAccount(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.openUSD("1.50", "3.00")
	h.linked(models.IntegrationStatusActive)
	row := usdRow(2, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_2").
		Return(providerView(row), nil).Once()

	updated := providerView(row)
	updated.FeePercent = "3.00"
	var sent ramp.VirtualAccountUpdate
	h.standing.EXPECT().UpdateVirtualAccount(testifymock.Anything, testifymock.Anything, "va_2", testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, _ string, req ramp.VirtualAccountUpdate) (*ramp.VirtualAccount, error) {
			sent = req
			return updated, nil
		}).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.FeePushed)
	require.Equal(t, "3.00", sent.FeePercent)
}

// While usd_payin has no provider, a USD account is not read at all: there is
// no fee to carry and no policy to compare it against. The Pix accounts beside
// it are swept as before.
func TestSyncVirtualAccounts_LeavesTheAccountsOfAClosedRailAlone(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	h.linked(models.IntegrationStatusActive)
	h.activeAccounts(usdRow(3, "1.50"))
	// No GetVirtualAccount expectation: the strict mock fails the test on one.

	report, err := h.syncer.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.Scanned)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 0, report.Failed)
}

// Production registers the MutualPay stub beside Bridge and Ozav, and the stub
// has no fee on any rail. The sweep must walk past it rather than abort: every
// Bridge account behind it would otherwise go unsynced on every run.
func TestSyncVirtualAccounts_SkipsAProviderWithoutARailFee(t *testing.T) {
	h := newSyncHarness(t, "1.50")
	provider := &standingAdapter{MockProviderAdapter: h.adapter, MockVirtualAccountAdapter: h.standing}
	h.syncer = rampsvc.NewVirtualAccountSyncer(
		ramp.NewFactory(provider, mutualpay.New()), h.integ, h.accounts, ramp.NewConfig(h.settings))
	h.linked(models.IntegrationStatusActive)
	row := standingRow(1, "1.50")
	h.activeAccounts(row)
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything, testifymock.Anything, "va_1").
		Return(providerView(row), nil).Once()

	report, err := h.syncer.Sync(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, report.Scanned)
	require.Equal(t, 0, report.FeePushed)
	h.accounts.AssertNotCalled(t, "FindActiveByProvider", models.ProviderMutualPay, testifymock.Anything)
}

// A company's account is synced against the company's own provider customer,
// never the one of the member who provisioned it (spec §5 F2): the sweep
// groups by customer, and asks about the customer the account is.
func TestSyncVirtualAccounts_AskAboutTheAccountsCustomer(t *testing.T) {
	const company = uint(1_000_000_000_007)
	h := newSyncHarness(t, "1.50")
	row := standingRow(1, "1.50")
	row.CustomerID = company
	h.activeAccounts(row)
	h.integ.EXPECT().FindByCustomerIDsAndProvider([]uint{company}, models.ProviderBridge).Return(
		[]models.CustomerProviderIntegration{{
			CustomerID: company, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
			ExternalCustomerID: support.Ptr("cus_company"), Metadata: "{}",
		}}, nil).Once()
	h.standing.EXPECT().GetVirtualAccount(testifymock.Anything,
		ramp.CustomerRef{CustomerID: company, ExternalID: "cus_company"}, row.ExternalAccountID).
		Return(providerView(row), nil).Once()

	report, err := h.syncer.Sync(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, report.Scanned)
	require.Zero(t, report.Failed)
}
