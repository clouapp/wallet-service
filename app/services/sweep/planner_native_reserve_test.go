package sweep

import (
	"context"
	"math/big"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// On a network that prices state creation (Sepolia since 2026-10), a native
// transfer that creates its recipient's account needs about ten times the gas of
// one to an existing account: 207,391 against 21,000, padded by 20 %.
const (
	reserveFreshRecipient = "0x00000000000000000000000000000000000000f1"
	reserveFreshLimit     = uint64(248_869)
	reserveExistingLimit  = uint64(25_200)
	reserveAmount         = int64(1_000_000_000_000_000) // 0.001
)

// reserveFee is limit × the port's 2 gwei gas price.
func reserveFee(limit uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(limit), big.NewInt(2_000_000_000))
}

func reservePlanner(t *testing.T, balances map[string]*big.Int, children ...string) (*service, *evmGasFixture) {
	t.Helper()
	fixture := newEVMGasFixture(t)
	fixture.adapter.nativeGasLimit = reserveExistingLimit
	fixture.adapter.recipientGasLimits = map[string]uint64{reserveFreshRecipient: reserveFreshLimit, "": reserveFreshLimit}
	fixture.adapter.balances = balances
	svc := fixture.plannerService()
	addresses := []models.Address{fixture.base}
	for _, child := range children {
		addresses = append(addresses, models.Address{ID: uuid.New(), WalletID: fixture.wallet.ID, Address: child})
	}
	svc.addressRepo = &fakeAddressRepo{children: addresses}
	return svc, fixture
}

func planNativeTo(t *testing.T, svc *service, fixture *evmGasFixture, amount *big.Int, to string) *Plan {
	t.Helper()
	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanNative, amount, to, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlan_EVMNative_ReservesTheGasItsRecipientNeeds(t *testing.T) {
	amount := big.NewInt(reserveAmount)
	enoughForExisting := new(big.Int).Add(amount, reserveFee(reserveExistingLimit))
	svc, fixture := reservePlanner(t, map[string]*big.Int{gasPlanBase: enoughForExisting})

	if plan := planNativeTo(t, svc, fixture, amount, gasPlanDestination); plan.Strategy != StrategyDirectFromBase ||
		plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(reserveFee(reserveExistingLimit)) != 0 {
		t.Fatalf("an existing recipient: strategy %s gas %v, want direct with %s", plan.Strategy, plan.EstimatedGas, reserveFee(reserveExistingLimit))
	}
	for _, to := range []string{reserveFreshRecipient, ""} {
		if plan := planNativeTo(t, svc, fixture, amount, to); plan.Strategy != StrategyInsufficient {
			t.Fatalf("recipient %q: strategy %s, want insufficient: the base cannot pay the account creation", to, plan.Strategy)
		}
	}

	enoughForFresh := new(big.Int).Add(amount, reserveFee(reserveFreshLimit))
	svc, fixture = reservePlanner(t, map[string]*big.Int{gasPlanBase: enoughForFresh})
	if plan := planNativeTo(t, svc, fixture, amount, reserveFreshRecipient); plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("a base holding amount + the account-creation gas is direct, got %s", plan.Strategy)
	}
	if !slices.Contains(fixture.adapter.reserveRecipients, reserveFreshRecipient) {
		t.Fatalf("the reserve must be priced for the withdrawal recipient, asked for %v", fixture.adapter.reserveRecipients)
	}
}

func TestPlan_EVMNative_SweepsKeepTheGasOfATransferIntoBase(t *testing.T) {
	// The withdrawal pays a fresh recipient; the sweep pays the existing base
	// address. Base holds half the amount and the child the other half plus both
	// fees, so they fit exactly when the sweep keeps only a transfer into base.
	const child = "0x00000000000000000000000000000000000000c1"
	amount := big.NewInt(reserveAmount)
	half := new(big.Int).Div(amount, big.NewInt(2))
	childBalance := new(big.Int).Add(half, reserveFee(reserveFreshLimit))
	childBalance.Add(childBalance, reserveFee(reserveExistingLimit))
	svc, fixture := reservePlanner(t, map[string]*big.Int{gasPlanBase: half, child: childBalance}, child)

	plan := planNativeTo(t, svc, fixture, amount, reserveFreshRecipient)
	if plan.Strategy != StrategyMultiSweep || len(plan.Sweeps) != 1 {
		t.Fatalf("plan %+v, want one sweep", plan)
	}
	if want := new(big.Int).Sub(childBalance, reserveFee(reserveExistingLimit)); plan.Sweeps[0].Amount.Cmp(want) != 0 {
		t.Fatalf("the sweep moves %s, want the child balance less a transfer into base = %s", plan.Sweeps[0].Amount, want)
	}
	if !slices.Contains(fixture.adapter.reserveRecipients, gasPlanBase) {
		t.Fatalf("the sweep reserve must be priced for the base address, asked for %v", fixture.adapter.reserveRecipients)
	}
}
