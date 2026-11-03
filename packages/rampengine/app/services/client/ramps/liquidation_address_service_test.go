package ramps_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// payoutAdapter is Bridge as the router resolves it and as the service
// type-asserts it: the fat ProviderAdapter and the liquidation-address port on
// one type, the way the real adapter is.
type payoutAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockLiquidationAddressAdapter
}

func (a *payoutAdapter) Code() ramp.ProviderCode { return a.MockProviderAdapter.Code() }

const bankAccountID = uint(12)

var bridgeRef = ramp.CustomerRef{CustomerID: userID, ExternalID: "cus_1"}

// payoutUser owns the bank account; payoutCustomer is their individual
// customer, whose id is theirs, so the harness's integration answers both.
func payoutUser() *models.User {
	user := &models.User{}
	user.ID = userID
	return user
}

func payoutCustomer() *models.Customer {
	return &models.Customer{Model: orm.Model{ID: userID}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

// laHarness wires the real router over mocked repositories: both USD payout
// rails are served by Bridge, in sandbox, for a user approved with `base`.
type laHarness struct {
	service      *ramps.LiquidationAddressService
	deps         ramps.Deps
	payout       *payoutAdapter
	settings     *mocks.MockSettingsSource
	banks        *mocks.MockUserBankAccountRepository
	destinations *mocks.MockBankAccountDestinationRepository
	addresses    *mocks.MockUserLiquidationAddressRepository
}

func newPayoutAddressHarness(t *testing.T) *laHarness {
	t.Helper()
	mock.Factory().Log()

	h := &laHarness{
		payout: &payoutAdapter{
			MockProviderAdapter:           mocks.NewMockProviderAdapter(t),
			MockLiquidationAddressAdapter: mocks.NewMockLiquidationAddressAdapter(t),
		},
		settings:     mocks.NewMockSettingsSource(t),
		banks:        mocks.NewMockUserBankAccountRepository(t),
		destinations: mocks.NewMockBankAccountDestinationRepository(t),
		addresses:    mocks.NewMockUserLiquidationAddressRepository(t),
	}
	h.payout.MockProviderAdapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	h.payout.MockProviderAdapter.EXPECT().Supports(testifymock.Anything).Return(true).Maybe()

	for _, rail := range []ramp.Rail{ramp.RailUSDACHPayout, ramp.RailUSDWirePayout} {
		group := ramp.RailGroup(rail)
		h.settings.EXPECT().String(group, ramp.SettingProviderOrder).Return(models.ProviderBridge, nil).Maybe()
		h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("1.00", nil).Maybe()
		h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingMinAmountUSD).Return("2.00", nil).Maybe()
	}
	bridgeGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(bridgeGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(bridgeGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()

	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	integ.EXPECT().FindByCustomerAndProvider(userID, models.ProviderBridge).Return(&models.CustomerProviderIntegration{
		CustomerID: userID, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
		ExternalCustomerID: support.Ptr("cus_1"), Metadata: "{}",
	}, nil).Maybe()
	kycs := mocks.NewMockCustomerProviderKycRepository(t)
	approved := &models.CustomerProviderKyc{CustomerID: userID, Provider: models.ProviderBridge, KycStatus: models.KycStatusApproved}
	require.NoError(t, approved.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementBase, Status: models.EndorsementStatusApproved},
	}))
	kycs.EXPECT().FindByCustomerAndProvider(userID, models.ProviderBridge).Return(approved, nil).Maybe()

	config := ramp.NewConfig(h.settings)
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: ramp.NewFactory(h.payout), Config: config,
		Integrations: integ, Kyc: kycs,
		Persons: ramp.Persons{Customers: support.IndividualCustomers(t), Profiles: noRetainedProfile(t)}, AppEnv: "local",
	})
	h.deps = ramps.Deps{Router: router, Config: config, Locker: passthroughLocker{}, Features: openFeatures(),
		Members: support.IndividualMembers(t)}
	h.rebuild()
	return h
}

// rebuild rewires the service after a test swapped one of h.deps.
func (h *laHarness) rebuild() {
	h.service = ramps.NewLiquidationAddressService(h.deps, h.banks, h.destinations, h.addresses)
}

func achInput() dto.ProvisionPayoutAddressDTO {
	return dto.ProvisionPayoutAddressDTO{
		Rail: models.RailUSDACHPayout, BankAccountID: bankAccountID,
		Token: "USDC", Chain: "solana", Wallet: wallet, Reference: support.Ptr("XIP 001"),
	}
}

func usBankAccount() *repositories.UserBankAccountView {
	return &repositories.UserBankAccountView{
		ID: bankAccountID, Label: "Minha conta corrente", OwnerName: "Ada King Lovelace", BankName: "Lead Bank",
		RoutingNumber: "021000021", AccountNumber: "000123456789", RoutingLast4: "0021", AccountLast4: "6789",
		AccountType: models.BankAccountTypeChecking,
		Address: models.BankAccountAddress{
			StreetLine1: "923 Folsom Street", City: "San Francisco", State: "CA", PostalCode: "94107", Country: "USA",
		},
	}
}

func activeAddress(rail string) *models.UserLiquidationAddress {
	reference, fee := "XIP 001", "1.00"
	return &models.UserLiquidationAddress{
		Model: orm.Model{ID: 5}, CustomerID: userID, UserID: userID, Provider: models.ProviderBridge, UserBankAccountID: bankAccountID,
		Rail: rail, Chain: "solana", Currency: "usdc", ExternalAddressID: "la_1", Address: "BridgeLiquidationAddr1",
		ReturnAddress: wallet, Reference: &reference, FeePercent: &fee,
		Status: models.LiquidationAddressStatusActive, RawPayload: "{}",
	}
}

func bridgeAddress() *ramp.LiquidationAddress {
	return &ramp.LiquidationAddress{
		ProviderAddressID: "la_1", Address: "BridgeLiquidationAddr1", Status: models.LiquidationAddressStatusActive,
		Rail: ramp.RailUSDACHPayout, Chain: "solana", Currency: "usdc", ExternalAccountID: "ext_1",
		Reference: "XIP 001", FeePercent: "1.00", Raw: []byte(`{"id":"la_1"}`),
	}
}

// expectReservation: the route has no address (read outside the lock, under
// it, and again to finalize); the reservation is written as id 5.
func (h *laHarness) expectReservation(rail string) {
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, rail, "solana", "usdc").Return(nil, nil).Times(3)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, rail, "solana", "usdc").Return(nil, nil).Once()
	var reserved *models.UserLiquidationAddress
	h.addresses.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		row.ID = 5
		reserved = row
		return nil
	}).Once()
	h.addresses.EXPECT().FindByID(uint(5)).RunAndReturn(func(uint) (*models.UserLiquidationAddress, error) {
		return reserved, nil
	}).Maybe()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(nil, nil).Maybe()
}

// expectReservationWithoutFinalize is a reservation the provider call never
// turns into an address.
func (h *laHarness) expectReservationWithoutFinalize(rail string) {
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, rail, "solana", "usdc").Return(nil, nil).Times(2)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, rail, "solana", "usdc").Return(nil, nil).Once()
	h.addresses.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		row.ID = 5
		return nil
	}).Once()
}

// The first address for a bank account registers the account at the provider
// (first and last name split off the holder), records the provider's id, then
// provisions over the rail asked.
func TestProvisionPayoutAddressRegistersTheAccountThenProvisions(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.expectReservation(models.RailUSDACHPayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(nil, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().RegisterBankAccount(testifymock.Anything, bridgeRef,
		ramp.BankAccountRegistration{
			OwnerName: "Ada King Lovelace", OwnerFirstName: "Ada King", OwnerLastName: "Lovelace",
			BankName: "Lead Bank", AccountName: "Minha conta corrente",
			RoutingNumber: "021000021", AccountNumber: "000123456789", AccountType: "checking",
			Address: usBankAccount().Address, IdempotencyKey: "bank:7:12",
		}).Return(&ramp.RegisteredBankAccount{ExternalAccountID: "ext_1", PaymentRails: []string{"ach", "ach_same_day"}}, nil).Once()
	h.destinations.EXPECT().Create(&models.BankAccountDestination{
		CustomerID: userID, UserBankAccountID: bankAccountID, Provider: models.ProviderBridge,
		DestinationRef: "ext_1", PaymentRails: `["ach","ach_same_day"]`,
	}).Return(nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef,
		ramp.LiquidationAddressRequest{
			Rail: ramp.RailUSDACHPayout, Source: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana"},
			ExternalAccountID: "ext_1", ReturnAddress: wallet, Reference: "XIP 001", FeePercent: "1.00",
			IdempotencyKey: "la:7:12:usd_ach_payout:solana:usdc:5",
		}).Return(bridgeAddress(), nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		require.Equal(t, "la_1", row.ExternalAddressID)
		require.Equal(t, "BridgeLiquidationAddr1", row.Address)
		require.Equal(t, models.LiquidationAddressStatusActive, row.Status)
		require.Equal(t, `{"id":"la_1"}`, row.RawPayload)
		return nil
	}).Once()

	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, &ramps.PayoutAddressView{
		Rail: models.RailUSDACHPayout, BankAccountID: bankAccountID, Token: "USDC", Chain: "solana",
		Address: "BridgeLiquidationAddr1", Reference: "XIP 001", FeePercent: "1.00", MinAmount: "2.00",
		DepositMode: ramps.DepositModeSimulated, Status: models.LiquidationAddressStatusActive,
	}, view)
}

// A bank account the provider already registered is not registered again:
// Bridge masks the number, so a second registration is a second account.
func TestProvisionPayoutAddressReusesTheRegisteredAccount(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.expectReservation(models.RailUSDACHPayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(&models.BankAccountDestination{
		DestinationRef: "ext_1", PaymentRails: "[]",
	}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef,
		testifymock.MatchedBy(func(req ramp.LiquidationAddressRequest) bool { return req.ExternalAccountID == "ext_1" })).
		Return(bridgeAddress(), nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	_, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.True(t, created)
}

// Two provisions of the same account over ACH and Wire both register it; the
// loser of the race on bank_account_destinations_unique reads the winner's row
// back and goes on, instead of failing with the reservation stuck.
func TestProvisionPayoutAddressReadsBackARegistrationAnotherRequestRecorded(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.expectReservation(models.RailUSDACHPayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(nil, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().RegisterBankAccount(testifymock.Anything, bridgeRef, testifymock.Anything).
		Return(&ramp.RegisteredBankAccount{ExternalAccountID: "ext_1"}, nil).Once()
	h.destinations.EXPECT().Create(testifymock.Anything).Return(repositories.ErrDuplicateBankAccountDestination).Once()
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(&models.BankAccountDestination{
		DestinationRef: "ext_1", PaymentRails: `["ach"]`,
	}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef,
		testifymock.MatchedBy(func(req ramp.LiquidationAddressRequest) bool { return req.ExternalAccountID == "ext_1" })).
		Return(bridgeAddress(), nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	_, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.True(t, created)
}

// A failure while registering the account happens before the address is asked
// for: nothing exists upstream, so the reservation is released whatever the
// error, and the user is not told provisioning_in_flight on the retry.
func TestProvisionPayoutAddressReleasesTheReservationWhenTheRegistrationFails(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.expectReservationWithoutFinalize(models.RailUSDACHPayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(nil, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().RegisterBankAccount(testifymock.Anything, bridgeRef, testifymock.Anything).
		Return(nil, errors.New("bridge: 500")).Once()
	h.addresses.EXPECT().Delete(testifymock.MatchedBy(func(row *models.UserLiquidationAddress) bool { return row.ID == 5 })).
		Return(nil).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.Error(t, err)
}

// The create's idempotency key names the reservation: a reservation dropped
// past its window, or a route provisioned again after its address went
// inactive, asks Bridge with a new key, since the body (wallet, reference) may
// differ; within one reservation the key does not move.
func TestProvisionPayoutAddressKeysTheCreateByReservation(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Times(3)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(&models.UserLiquidationAddress{
			Model:  orm.Model{ID: 4, Timestamps: orm.Timestamps{CreatedAt: carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-time.Hour)))}},
			Status: models.LiquidationAddressStatusProvisioning,
		}, nil).Once()
	h.addresses.EXPECT().Delete(testifymock.MatchedBy(func(row *models.UserLiquidationAddress) bool { return row.ID == 4 })).
		Return(nil).Once()
	var reserved *models.UserLiquidationAddress
	h.addresses.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		row.ID, reserved = 9, row
		return nil
	}).Once()
	h.addresses.EXPECT().FindByID(uint(9)).RunAndReturn(func(uint) (*models.UserLiquidationAddress, error) {
		return reserved, nil
	}).Once()
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(nil, nil).Once()
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).
		Return(&models.BankAccountDestination{DestinationRef: "ext_1", PaymentRails: "[]"}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef,
		testifymock.MatchedBy(func(req ramp.LiquidationAddressRequest) bool {
			return req.IdempotencyKey == "la:7:12:usd_ach_payout:solana:usdc:9"
		})).Return(bridgeAddress(), nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	_, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.True(t, created)
}

// An address the user already holds is theirs to see while the rail is
// closed: the switch stops new addresses, not the one in their wallet app.
func TestProvisionPayoutAddressReturnsTheExistingAddressEvenWithTheRailClosed(t *testing.T) {
	h := newPayoutAddressHarness(t)
	features := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(features)
	require.NoError(t, features.Deactivate(context.Background(), ramp.RailUSDACHPayout.Flag(), nil))
	h.deps.Features = features
	h.rebuild()

	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "BridgeLiquidationAddr1", view.Address)
	require.Equal(t, "2.00", view.MinAmount)
}

func TestProvisionPayoutAddressRefusesANewAddressOnAClosedRail(t *testing.T) {
	h := newPayoutAddressHarness(t)
	features := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(features)
	require.NoError(t, features.Deactivate(context.Background(), ramp.RailUSDACHPayout.Flag(), nil))
	h.deps.Features = features
	h.rebuild()

	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(nil, nil).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramp.ErrRailDisabled)
}

// emptyACHOrder rewires the harness's settings so the ACH payout rail has no
// provider in its order: an operator closed it.
func emptyACHOrder(h *laHarness) {
	h.settings.ExpectedCalls = nil
	group := ramp.RailGroup(ramp.RailUSDACHPayout)
	h.settings.EXPECT().String(group, ramp.SettingProviderOrder).Return("", nil).Maybe()
	h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderBridge)).Return("1.00", nil).Maybe()
	h.settings.EXPECT().String(group, ramp.FeeSettingKey(ramp.ProviderOzav)).Return("9.99", nil).Maybe()
	h.settings.EXPECT().String(group, ramp.SettingMinAmountUSD).Return("2.00", nil).Maybe()
	bridgeGroup := ramp.ProviderGroup(models.ProviderBridge)
	h.settings.EXPECT().Bool(bridgeGroup, ramp.SettingEnabled).Return(true, nil).Maybe()
	h.settings.EXPECT().String(bridgeGroup, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
}

// An amount says the app is about to sign and send: on an existing address
// that is a payout being born, so a rail closed by its flag or by an empty
// provider order refuses it, as it refuses a new address. Nothing is synced.
func TestProvisionPayoutAddressRefusesAnAmountOnAClosedRail(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		h := newPayoutAddressHarness(t)
		features := featureflag.NewManager(drivers.NewMemory())
		catalog.Register(features)
		require.NoError(t, features.Deactivate(context.Background(), ramp.RailUSDACHPayout.Flag(), nil))
		h.deps.Features = features
		h.rebuild()
		h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
		h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
			Return(activeAddress(models.RailUSDACHPayout), nil).Once()

		in := achInput()
		in.Reference, in.Amount = support.Ptr("RENT 10"), "50.00"
		_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
		require.ErrorIs(t, err, ramp.ErrRailDisabled)
	})
	t.Run("no provider in the order", func(t *testing.T) {
		h := newPayoutAddressHarness(t)
		emptyACHOrder(h)
		h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
		h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
			Return(activeAddress(models.RailUSDACHPayout), nil).Once()

		in := achInput()
		in.Reference, in.Amount = support.Ptr("RENT 10"), "50.00"
		_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
		require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	})
}

// Without an amount the existing address is only being shown: a closed rail
// still answers it, so the user keeps seeing where their crypto would go.
func TestProvisionPayoutAddressShowsAnExistingAddressOnAClosedRail(t *testing.T) {
	h := newPayoutAddressHarness(t)
	emptyACHOrder(h)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "BridgeLiquidationAddr1", view.Address)
}

// A changed reference is written through to the provider's address, on the
// address's own provider, and persisted from the provider's answer.
func TestProvisionPayoutAddressWritesAChangedReferenceThrough(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	reference := "RENT 10"
	updated := bridgeAddress()
	updated.Reference, updated.Raw = reference, []byte(`{"id":"la_1","destination_ach_reference":"RENT 10"}`)
	h.payout.MockLiquidationAddressAdapter.EXPECT().UpdateLiquidationAddress(testifymock.Anything, bridgeRef, "la_1",
		ramp.LiquidationAddressUpdate{Reference: &reference}).Return(updated, nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		require.Equal(t, "RENT 10", *row.Reference)
		require.JSONEq(t, `{"id":"la_1","destination_ach_reference":"RENT 10"}`, row.RawPayload)
		return nil
	}).Once()

	in := achInput()
	in.Reference = &reference
	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "RENT 10", view.Reference)
}

func TestProvisionPayoutAddressLeavesAnUnchangedReferenceAlone(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "XIP 001", view.Reference)
}

// An amount named before the send is held to the rail's minimum after XIP's
// fee: 2.00 USDC at 1% pays 1.98, under the 2.00 floor, and is refused with
// the floor; the address itself stays.
func TestProvisionPayoutAddressRefusesAnAmountBelowTheMinimumAfterTheFee(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	in := achInput()
	in.Amount = "2.00"
	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	require.Equal(t, "2.00", violation.LimitUSD)
	require.Empty(t, violation.LimitBRL)
}

// The amount is checked before the provider hears of anything: a refused
// amount must not leave a new address, a registered bank account or a
// reservation behind. The mocks are strict, so any create upstream or any
// reservation row fails the test.
func TestProvisionPayoutAddressRefusesAnAmountBelowTheMinimumBeforeCreatingAnything(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(nil, nil).Times(2)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(nil, nil).Maybe()

	in := achInput()
	in.Amount = "2.00"
	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
}

// A refused amount does not write a changed reference through either: the
// existing address stays as the provider holds it.
func TestProvisionPayoutAddressRefusesAnAmountBelowTheMinimumBeforeSyncingTheAddress(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	in := achInput()
	in.Reference = support.Ptr("RENT 10")
	in.Amount = "2.00"
	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
}

func TestProvisionPayoutAddressTakesAnAmountThatClearsTheMinimum(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	in := achInput()
	in.Amount = "2.03"
	view, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.NoError(t, err)
	require.Equal(t, "BridgeLiquidationAddr1", view.Address)
}

// The bank account is the member's own: a company paying it would be the
// payout to a member's personal account the money rules forbid on Pix. Until
// business payouts are designed, acting for a company is refused before
// anything is read or asked, for an address and for a simulated deposit.
func TestProvisionPayoutAddressRefusesABusiness(t *testing.T) {
	h := newPayoutAddressHarness(t)
	company := &models.Customer{Model: orm.Model{ID: businessID}, Type: models.CustomerTypeBusiness, Status: models.CustomerStatusActive}

	_, _, err := h.service.Provision(context.Background(), payoutUser(), company, achInput())
	require.ErrorIs(t, err, ramps.ErrBusinessPayoutUnsupported)
	_, err = h.service.SimulateDeposit(context.Background(), payoutUser(), company, simulateInput())
	require.ErrorIs(t, err, ramps.ErrBusinessPayoutUnsupported)
}

// Another process won the route between the read and the insert: the unique
// says so, and the user is told provisioning is in flight, not a 500.
func TestProvisionPayoutAddressAnswersALostRouteRaceAsInFlight(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Times(2)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Once()
	h.addresses.EXPECT().Create(testifymock.Anything).Return(repositories.ErrDuplicateLiquidationAddressRoute).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramps.ErrProvisioningInFlight)
}

// The account was deleted after the request read it: the reservation is
// refused under the lock, with the repository's live-user check for a deleted
// account.
func TestProvisionPayoutAddressRefusesAReservationForAGoneAccount(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Times(2)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Once()
	h.addresses.EXPECT().Create(testifymock.Anything).Return(fmt.Errorf("repo: %w", repositories.ErrUserDeleted)).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramps.ErrAccountDeleted)
}

// The bank account was deleted while Bridge was creating the address. The
// address is recorded inactive (a drain to it is held for review, not
// unknown), the user is told the account is gone, and the registration is
// deactivated upstream again: the delete may have listed none yet.
func TestProvisionPayoutAddressStopsWhenTheAccountIsDeletedMidway(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil).Times(2)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(nil, nil).Once()
	h.expectReservation(models.RailUSDACHPayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).
		Return(&models.BankAccountDestination{DestinationRef: "ext_1", PaymentRails: "[]"}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef, testifymock.Anything).
		Return(bridgeAddress(), nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		require.Equal(t, "la_1", row.ExternalAddressID)
		require.Equal(t, models.LiquidationAddressStatusInactive, row.Status)
		return nil
	}).Once()
	h.destinations.EXPECT().ListByBankAccount(bankAccountID).Return([]models.BankAccountDestination{
		{CustomerID: userID, UserBankAccountID: bankAccountID, Provider: models.ProviderBridge, DestinationRef: "ext_1"},
	}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().DeactivateBankAccount(testifymock.Anything, bridgeRef, "ext_1").
		Return(nil).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramps.ErrBankAccountNotFound)
}

// Bridge answered an address a row already holds: an earlier provision of the
// route, gone inactive. That row takes the answer and is the address again,
// and the reservation is dropped first, so the two never both hold the route.
func TestProvisionPayoutAddressRevivesTheRowOfTheAddressBridgeAnswered(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Times(3)
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").Return(nil, nil).Once()
	var reserved *models.UserLiquidationAddress
	h.addresses.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		row.ID, reserved = 5, row
		return nil
	}).Once()
	h.addresses.EXPECT().FindByID(uint(5)).RunAndReturn(func(uint) (*models.UserLiquidationAddress, error) {
		return reserved, nil
	}).Once()
	old := activeAddress(models.RailUSDACHPayout)
	old.ID, old.Status, old.ReturnAddress = 3, models.LiquidationAddressStatusInactive, "OldWallet"
	h.addresses.EXPECT().FindByProviderAndExternalID(models.ProviderBridge, "la_1").Return(old, nil).Once()
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).
		Return(&models.BankAccountDestination{DestinationRef: "ext_1", PaymentRails: "[]"}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().ProvisionLiquidationAddress(testifymock.Anything, bridgeRef, testifymock.Anything).
		Return(bridgeAddress(), nil).Once()
	var order []string
	h.addresses.EXPECT().Delete(testifymock.MatchedBy(func(row *models.UserLiquidationAddress) bool { return row.ID == 5 })).
		RunAndReturn(func(*models.UserLiquidationAddress) error {
			order = append(order, "drop reservation")
			return nil
		}).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		order = append(order, "revive")
		require.Equal(t, uint(3), row.ID)
		require.Equal(t, models.LiquidationAddressStatusActive, row.Status)
		require.Equal(t, wallet, row.ReturnAddress)
		return nil
	}).Once()

	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, models.LiquidationAddressStatusActive, view.Status)
	require.Equal(t, []string{"drop reservation", "revive"}, order)
}

// A different wallet on an existing address moves its return address at the
// provider (Bridge's return_instructions), and the row keeps what it answered.
func TestProvisionPayoutAddressMovesAChangedReturnWallet(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	moved := bridgeAddress()
	moved.ReturnAddress = "NewWa11et"
	h.payout.MockLiquidationAddressAdapter.EXPECT().UpdateLiquidationAddress(testifymock.Anything, bridgeRef, "la_1",
		ramp.LiquidationAddressUpdate{ReturnAddress: "NewWa11et"}).Return(moved, nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).RunAndReturn(func(row *models.UserLiquidationAddress) error {
		require.Equal(t, "NewWa11et", row.ReturnAddress)
		require.Equal(t, "XIP 001", *row.Reference)
		return nil
	}).Once()

	in := achInput()
	in.Wallet = "NewWa11et"
	_, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.NoError(t, err)
	require.False(t, created)
}

// An omitted reference keeps the address's own: the app need not resend it,
// and Bridge is not asked to clear it.
func TestProvisionPayoutAddressKeepsTheReferenceWhenNoneIsSent(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()

	in := achInput()
	in.Reference = nil
	view, created, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "XIP 001", view.Reference)
}

// An empty reference sent on purpose is a change: it clears the address's.
func TestProvisionPayoutAddressClearsTheReferenceWhenAnEmptyOneIsSent(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(activeAddress(models.RailUSDACHPayout), nil).Once()
	cleared := bridgeAddress()
	cleared.Reference = ""
	h.payout.MockLiquidationAddressAdapter.EXPECT().UpdateLiquidationAddress(testifymock.Anything, bridgeRef, "la_1",
		ramp.LiquidationAddressUpdate{Reference: support.Ptr("")}).Return(cleared, nil).Once()
	h.addresses.EXPECT().Update(testifymock.Anything).Return(nil).Once()

	in := achInput()
	in.Reference = support.Ptr("")
	view, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.NoError(t, err)
	require.Empty(t, view.Reference)
}

// Bridge said the account takes no wire (its beneficiary address did not
// validate). Nothing was created upstream, so the reservation is released and
// the user may pick ACH.
func TestProvisionPayoutAddressRefusesAWireTheAccountDoesNotTake(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.expectReservationWithoutFinalize(models.RailUSDWirePayout)
	h.destinations.EXPECT().Find(userID, bankAccountID, models.ProviderBridge).Return(&models.BankAccountDestination{
		DestinationRef: "ext_1", PaymentRails: `["ach","ach_same_day"]`,
	}, nil).Once()
	h.addresses.EXPECT().Delete(testifymock.MatchedBy(func(row *models.UserLiquidationAddress) bool { return row.ID == 5 })).
		Return(nil).Once()

	in := achInput()
	in.Rail = models.RailUSDWirePayout
	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.ErrorIs(t, err, ramps.ErrPayoutRailUnsupportedForAccount)
}

// Another request holds the reservation and is talking to the provider: a
// second create would be a second address.
func TestProvisionPayoutAddressRefusesWhileAnotherIsInFlight(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(usBankAccount(), nil)
	h.addresses.EXPECT().FindActiveRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(nil, nil).Times(2)
	now := carbon.NewDateTime(carbon.FromStdTime(time.Now()))
	h.addresses.EXPECT().FindProvisioningRoute(userID, bankAccountID, models.RailUSDACHPayout, "solana", "usdc").
		Return(&models.UserLiquidationAddress{
			Model:  orm.Model{ID: 5, Timestamps: orm.Timestamps{CreatedAt: now}},
			Status: models.LiquidationAddressStatusProvisioning,
		}, nil).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramps.ErrProvisioningInFlight)
}

// Another user's bank account is indistinguishable from a missing one.
func TestProvisionPayoutAddressRefusesABankAccountTheUserDoesNotHold(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.banks.EXPECT().FindOwned(userID, bankAccountID).Return(nil, nil).Once()

	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), achInput())
	require.ErrorIs(t, err, ramps.ErrBankAccountNotFound)
}

// A rail that pays no bank account has no liquidation address.
func TestProvisionPayoutAddressRefusesAnotherRail(t *testing.T) {
	h := newPayoutAddressHarness(t)

	in := achInput()
	in.Rail = models.RailPixPayout
	_, _, err := h.service.Provision(context.Background(), payoutUser(), payoutCustomer(), in)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

func TestDeactivateBankAccountAddressesMarksThemInactive(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.addresses.EXPECT().DeactivateByBankAccount(userID, bankAccountID).Return(nil).Once()

	require.NoError(t, h.service.DeactivateBankAccountAddresses(userID, bankAccountID))
}

// Bridge has no deactivate for a liquidation address; deactivating the
// external account the account was registered as is what stops its addresses
// paying it.
func TestDeactivateBankAccountUpstreamDeactivatesEachRegistration(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.destinations.EXPECT().ListByBankAccount(bankAccountID).Return([]models.BankAccountDestination{
		{CustomerID: userID, UserBankAccountID: bankAccountID, Provider: models.ProviderBridge, DestinationRef: "ext_1"},
	}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().DeactivateBankAccount(testifymock.Anything, bridgeRef, "ext_1").
		Return(nil).Once()

	h.service.DeactivateBankAccountUpstream(context.Background(), bankAccountID)
}

// The provider failing is logged, not returned: the account is already
// deleted and its addresses inactive here, and a drain that still arrives is
// held for review by the webhook.
func TestDeactivateBankAccountUpstreamSurvivesAProviderFailure(t *testing.T) {
	h := newPayoutAddressHarness(t)
	h.destinations.EXPECT().ListByBankAccount(bankAccountID).Return([]models.BankAccountDestination{
		{CustomerID: userID, UserBankAccountID: bankAccountID, Provider: models.ProviderBridge, DestinationRef: "ext_1"},
	}, nil).Once()
	h.payout.MockLiquidationAddressAdapter.EXPECT().DeactivateBankAccount(testifymock.Anything, bridgeRef, "ext_1").
		Return(errors.New("bridge: 500")).Once()

	h.service.DeactivateBankAccountUpstream(context.Background(), bankAccountID)
}
