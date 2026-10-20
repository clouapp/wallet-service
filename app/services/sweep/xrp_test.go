package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fakexrpl"
)

const (
	xrpTestChildIndex = 25
	// The fake altnet: 1 XRP base reserve, 0.2 XRP per owned object, 12-drop fee.
	xrpTestFee         = 12
	xrpTestBaseReserve = 1_000_000
)

func randomXRPAddress(t *testing.T) string {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return mustAddress(t, models.ChainTXRP, true, key.PubKey().SerializeCompressed())
}

func newXRPAdapter(t *testing.T, node *fakexrpl.Server, broadcasts *[]*types.SignedTx) *chainfixtures.XRPAltnet {
	t.Helper()
	return chainfixtures.NewXRPAltnet(node.URL, func(_ context.Context, signed *types.SignedTx) (string, error) {
		*broadcasts = append(*broadcasts, signed)
		return signed.TxHash, nil
	})
}

func xrpChainEntity() *models.Chain {
	return &models.Chain{ID: models.ChainTXRP, AdapterType: models.AdapterTypeXRP, NativeSymbol: models.NativeXRP, NativeDecimals: 6}
}

// xrpPlannerFixture is an XRP wallet whose base and child addresses are funded on
// the fake altnet; the child holds far more than the base.
type xrpPlannerFixture struct {
	node   *fakexrpl.Server
	svc    *service
	wallet *models.Wallet
	child  models.Address
}

func newXRPPlannerFixture(t *testing.T, baseDrops string, ownerCount uint64) *xrpPlannerFixture {
	t.Helper()
	node := fakexrpl.New(t)
	var broadcasts []*types.SignedTx
	adapter := newXRPAdapter(t, node, &broadcasts)
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Chain: models.ChainTXRP, Address: randomXRPAddress(t), DerivationType: "genesis"}
	child := models.Address{ID: uuid.New(), WalletID: walletID, Chain: models.ChainTXRP, Address: randomXRPAddress(t), DerivationType: bip32DerivationType, DerivationIndex: 1}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainTXRP, DepositAddressID: &base.ID, DepositAddress: &base}
	node.Fund(base.Address, fakexrpl.Account{Balance: baseDrops, Sequence: 3, OwnerCount: ownerCount})
	node.Fund(child.Address, fakexrpl.Account{Balance: "500000000", Sequence: 1})

	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{base, child}},
		chainRepo:   &fakeChainRepo{chain: xrpChainEntity()},
	}
	return &xrpPlannerFixture{node: node, svc: svc, wallet: wallet, child: child}
}

func (f *xrpPlannerFixture) plan(t *testing.T, asset string, drops int64, to string) (*Plan, error) {
	t.Helper()
	return f.svc.PlanForWithdrawal(context.Background(), f.wallet.ID, asset, big.NewInt(drops), to, uuid.Nil)
}

func TestPlan_XRP_DirectFromBaseAndNeverFromChildren(t *testing.T) {
	// 10 XRP at base with one owned object: reserve 1.2 XRP, spendable 8.799988 XRP.
	f := newXRPPlannerFixture(t, "10000000", 1)
	dest := randomXRPAddress(t)
	f.node.Fund(dest, fakexrpl.Account{Balance: "30000000", Sequence: 1})

	plan, err := f.plan(t, models.NativeXRP, 8_799_988, dest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyDirectFromBase || plan.SourceAddress == nil || plan.SourceAddress.Address != f.wallet.DepositAddress.Address || !plan.ReachesTarget {
		t.Fatalf("plan %+v", plan)
	}
	requireBigInt(t, "estimated fee", plan.EstimatedGas, xrpTestFee)
	requireBigInt(t, "base balance", plan.BaseBalance, 10_000_000)

	plan, err = f.plan(t, models.NativeXRP, 8_799_989, dest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyInsufficient || plan.ReachesTarget || len(plan.Sweeps) != 0 || plan.SourceAddress != nil {
		t.Fatalf("a base short by one drop must not sweep the funded child: %+v", plan)
	}
	var shortfall *BaseReserveShortfallError
	if !errors.As(plan.Shortfall, &shortfall) || !errors.Is(plan.Shortfall, ErrInsufficientFunds) {
		t.Fatalf("shortfall %v", plan.Shortfall)
	}
	requireBigInt(t, "shortfall reserve", shortfall.Funds.Reserve, 1_200_000)
	for _, call := range f.node.Calls() {
		if call == "submit" {
			t.Fatal("the planner submitted a payment")
		}
	}
}

func TestPlan_XRP_RefusesDestinationsTheLedgerWouldRefuse(t *testing.T) {
	f := newXRPPlannerFixture(t, "50000000", 0)
	unfunded, tagged := randomXRPAddress(t), randomXRPAddress(t)
	f.node.Fund(tagged, fakexrpl.Account{Balance: "30000000", Sequence: 1, Flags: fakexrpl.RequireDestTag})

	if _, err := f.plan(t, models.NativeXRP, xrpTestBaseReserve-1, unfunded); !errors.Is(err, ErrDestinationBelowReserve) {
		t.Fatalf("unfunded destination below the base reserve: %v", err)
	}
	if plan, err := f.plan(t, models.NativeXRP, xrpTestBaseReserve, unfunded); err != nil || plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("unfunded destination at the base reserve: %+v %v", plan, err)
	}
	if _, err := f.plan(t, models.NativeXRP, 5_000_000, tagged); !errors.Is(err, ErrDestinationTagRequired) {
		t.Fatalf("destination requiring a tag: %v", err)
	}
	if _, err := f.plan(t, models.SymbolUSDT, 5_000_000, unfunded); !errors.Is(err, ErrUnsupportedChain) {
		t.Fatalf("issued currency: %v", err)
	}
}

// mainnetXRP is an XRP adapter that reports mainnet; the planner refuses it before
// reading the ledger.
type mainnetXRP struct{ *chainfixtures.XRPAltnet }

func (mainnetXRP) IsTestnet() bool { return false }

func TestPlan_XRP_MainnetIsUnsupported(t *testing.T) {
	f := newXRPPlannerFixture(t, "50000000", 0)
	var broadcasts []*types.SignedTx
	registry := chain.NewRegistry()
	registry.RegisterChain(mainnetXRP{newXRPAdapter(t, f.node, &broadcasts)})
	f.svc.registry = registry

	if _, err := f.plan(t, models.NativeXRP, 5_000_000, randomXRPAddress(t)); !errors.Is(err, ErrUnsupportedChain) {
		t.Fatalf("mainnet: %v", err)
	}
	if calls := f.node.Calls(); len(calls) != 0 {
		t.Fatalf("mainnet plan reached the server: %v", calls)
	}
}

func TestPlan_XRP_IsNotConsolidated(t *testing.T) {
	f := newXRPPlannerFixture(t, "50000000", 0)
	if _, err := f.svc.PreflightConsolidation(context.Background(), f.wallet.ID, models.NativeXRP, preflightTestPassphrase); !errors.Is(err, ErrUnsupportedChain) {
		t.Fatalf("consolidation: %v", err)
	}
}

func TestQuote_XRP_PricesThePaymentFromBase(t *testing.T) {
	f := newXRPPlannerFixture(t, "10000000", 2)
	dest := randomXRPAddress(t)
	f.node.Fund(dest, fakexrpl.Account{Balance: "30000000", Sequence: 1})

	got := quote(t, f.svc, f.wallet.ID, models.NativeXRP, 2_000_000, dest)
	if got.Strategy != StrategyDirectFromBase || got.Basis != FeeBasisPlan || !got.AmountSpendable || got.Transfers != 1 || got.RecipientIsProbe {
		t.Fatalf("quote %+v", got)
	}
	requireBigInt(t, "fee", got.Fee, xrpTestFee)
	requireBigInt(t, "minimum remaining (1 XRP + 2 × 0.2 XRP)", got.MinimumRemaining, 1_400_000)
	if got.XRP == nil {
		t.Fatal("no xrp details")
	}
	requireBigInt(t, "base reserve", got.XRP.BaseReserve, xrpTestBaseReserve)
	requireBigInt(t, "account reserve", got.XRP.AccountReserve, 1_400_000)

	unfunded := quote(t, f.svc, f.wallet.ID, models.NativeXRP, 9_000_000, "")
	if unfunded.Basis != FeeBasisUnfundedDirect || unfunded.AmountSpendable || !unfunded.RecipientIsProbe {
		t.Fatalf("unfunded quote %+v", unfunded)
	}
	requireBigInt(t, "unfunded fee", unfunded.Fee, xrpTestFee)

	if _, err := f.svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{
		WalletID: f.wallet.ID, Asset: models.NativeXRP, Amount: big.NewInt(1), ToAddress: randomXRPAddress(t),
	}); !errors.Is(err, ErrDestinationBelowReserve) {
		t.Fatalf("quote to an unfunded destination below the reserve: %v", err)
	}
}

// newXRPSigningFixture is a real 2-of-2 MPC wallet on the fake altnet with its base
// address funded; broadcasts are recorded, never sent.
func newXRPSigningFixture(t *testing.T) (*secp256k1WalletFixture, *chainfixtures.XRPAltnet, *fakexrpl.Server, *[]*types.SignedTx) {
	t.Helper()
	node := fakexrpl.New(t)
	broadcasts := &[]*types.SignedTx{}
	adapter := newXRPAdapter(t, node, broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, xrpTestChildIndex)
	node.Fund(fixture.wallet.DepositAddress.Address, fakexrpl.Account{Balance: "25000000", Sequence: 11})
	return fixture, adapter, node, broadcasts
}

func TestExecutePlan_XRPBaseIsSignedByTheThresholdCeremony(t *testing.T) {
	fixture, adapter, node, broadcasts := newXRPSigningFixture(t)
	base := *fixture.wallet.DepositAddress
	dest := randomXRPAddress(t)
	node.Fund(dest, fakexrpl.Account{Balance: "30000000", Sequence: 1})
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTXRP, Asset: models.NativeXRP,
		Amount: big.NewInt(3_000_000), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	svc := fixture.executor(adapter)
	result, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), dest, "user-3119")
	if err != nil {
		t.Fatal(err)
	}
	if len(*broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(*broadcasts))
	}
	signed := (*broadcasts)[0]
	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: base.Address, To: dest, Amount: big.NewInt(3_000_000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, base.Address); err != nil {
		t.Fatalf("broadcast payment does not verify as the base address: %v", err)
	}
	row := result.FinalWithdrawTx
	if row == nil || row.TxType != models.TxTypeWithdrawal || row.Chain != models.ChainTXRP || row.TxHash != signed.TxHash ||
		row.FromAddress != base.Address || row.ToAddress != dest || row.Amount != "3000000" || row.Status != string(types.TxStatusConfirming) {
		t.Fatalf("withdrawal row %+v", row)
	}
}

func TestPreflight_Withdrawal_XRPBaseIsSignedVerifiedAndNotBroadcast(t *testing.T) {
	fixture, adapter, node, broadcasts := newXRPSigningFixture(t)
	svc, txRepo := preflightService(t, fixture, adapter)
	svc.chainRepo = &fakeChainRepo{chain: xrpChainEntity()}
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{*fixture.wallet.DepositAddress, fixture.child}}
	dest := randomXRPAddress(t)
	node.Fund(dest, fakexrpl.Account{Balance: "30000000", Sequence: 1})

	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, models.NativeXRP, big.NewInt(4_000_000), dest, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.PreflightWithdrawal(context.Background(), plan, preflightTestPassphrase, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(*broadcasts) != 0 || len(txRepo.created) != 0 {
		t.Fatalf("preflight broadcast %d payments and persisted %d rows", len(*broadcasts), len(txRepo.created))
	}
	if len(result.Transactions) != 1 || result.Strategy != StrategyDirectFromBase {
		t.Fatalf("preflight %+v", result)
	}
	signed := result.Transactions[0]
	if signed.Role != preflightRoleWithdrawal || signed.From != fixture.wallet.DepositAddress.Address || signed.To != dest || len(signed.TxHash) != 64 {
		t.Fatalf("preflight tx %+v", signed)
	}
	requireBigInt(t, "preflight fee", result.EstimatedGas, xrpTestFee)
	for _, call := range node.Calls() {
		if call == "submit" {
			t.Fatal("preflight submitted a payment")
		}
	}
}

func TestPlan_XRP_ATaggedWithdrawalReachesAnAccountThatRequiresATag(t *testing.T) {
	f := newXRPPlannerFixture(t, "50000000", 0)
	exchange := randomXRPAddress(t)
	f.node.Fund(exchange, fakexrpl.Account{Balance: "30000000", Sequence: 1, Flags: fakexrpl.RequireDestTag})
	tag := uint32(0)

	plan, err := f.svc.PlanForWithdrawalTo(context.Background(), f.wallet.ID, models.NativeXRP, big.NewInt(5_000_000),
		Destination{Address: exchange, Tag: &tag}, uuid.Nil)
	if err != nil {
		t.Fatalf("tag 0 is a tag: %v", err)
	}
	if plan.Strategy != StrategyDirectFromBase || plan.DestinationTag == nil || *plan.DestinationTag != 0 {
		t.Fatalf("plan %+v", plan)
	}
	quoted, err := f.svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{
		WalletID: f.wallet.ID, Asset: models.NativeXRP, Amount: big.NewInt(5_000_000), ToAddress: exchange, DestinationTag: &tag,
	})
	if err != nil || quoted.Fee.Int64() != xrpTestFee {
		t.Fatalf("tagged quote %+v err %v", quoted, err)
	}
}

func TestPlan_ADestinationTagOnAnotherChainIsRefused(t *testing.T) {
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &base}
	mockChain := balanceMapChain("eth", models.NativeETH, map[string]*big.Int{"BASE": big.NewInt(1_000_000)})
	svc := newPlannerService(t, wallet, nil, mockChain, evmChainEntity("eth"))
	tag := uint32(7)

	_, err := svc.PlanForWithdrawalTo(context.Background(), walletID, models.NativeETH, big.NewInt(10), Destination{Address: "0xdest", Tag: &tag}, uuid.Nil)
	if !errors.Is(err, ErrDestinationTagUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutePlan_XRPTaggedPaymentRecordsTheTagAndItsLastLedger(t *testing.T) {
	fixture, adapter, node, broadcasts := newXRPSigningFixture(t)
	base := *fixture.wallet.DepositAddress
	exchange := randomXRPAddress(t)
	node.Fund(exchange, fakexrpl.Account{Balance: "30000000", Sequence: 1, Flags: fakexrpl.RequireDestTag})
	tag := uint32(98765)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTXRP, Asset: models.NativeXRP,
		Amount: big.NewInt(3_000_000), Strategy: StrategyDirectFromBase, SourceAddress: &base, DestinationTag: &tag}

	result, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), exchange, "user-3119")
	if err != nil {
		t.Fatal(err)
	}
	if len(*broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(*broadcasts))
	}
	signed := (*broadcasts)[0]
	tagged, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: base.Address, To: exchange, Amount: big.NewInt(3_000_000), DestinationTag: &tag})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.VerifySignedTransaction(tagged, signed, base.Address); err != nil {
		t.Fatalf("the broadcast payment is not the tagged one: %v", err)
	}
	row := result.FinalWithdrawTx
	if row.DestinationTag == nil || *row.DestinationTag != tag {
		t.Fatalf("row tag %v", row.DestinationTag)
	}
	if row.RawPayload != `{"broadcast_outcome":"accepted","last_ledger_sequence":120}` {
		t.Fatalf("raw payload %s", row.RawPayload)
	}
}
