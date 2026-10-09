package sweep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
)

const (
	tronTestDestination = "TL1eeYCiqPwERsRVdscUBEWi5aHvnwtvfh"
	tronTestChildIndex  = 25
	// Live Nile figures: USDT transfer energy (estimateenergy) and the bandwidth of
	// the transfers BuildTransfer writes.
	tronTestUSDTEnergy        = 21_975
	tronTestUSDTFee           = 2_637_000 + 345_000
	tronTestTRXFeeExisting    = 267_000
	tronTestActivationFee     = 1_100_000
	tronTestReferenceUSDTFee  = 15_634_200 + 345_000
	tronTestHeadBlockID       = "00000000044380e58a4462a4d528c77ed871164034c42047cdbecfce85ca8ce0"
	tronTestIncludedBlock     = 71532800
	tronTestUSDTSymbolOnChain = models.SymbolUSDT
	tronTestFullUserPercent   = 100
	tronTestOriginHex         = "4166b9c0ab3a5a1a1a3d8a8e0a7d6a1a2a3b4c5d6e"
	tronTestOriginEnergyLimit = 1_000_000_000
	// TRON protobuf fields: Transaction.raw_data, raw.contract, Contract.parameter,
	// Any.value and TransferContract's owner, recipient and amount.
	tronPBRawData      = 1
	tronPBContract     = 11
	tronPBParameter    = 2
	tronPBAnyValue     = 2
	tronPBTransferFrom = 1
	tronPBTransferTo   = 2
	tronPBTransferSun  = 3
)

var tronTestUSDT = types.Token{Symbol: tronTestUSDTSymbolOnChain, Name: "Tether USD", Contract: models.USDTContractTronNile, Decimals: 6, ChainID: models.ChainTron}

// fakeTronNode answers the read-only java-tron API from in-memory balances and
// records the order in which transactions were broadcast and awaited.
type fakeTronNode struct {
	t        *testing.T
	mu       sync.Mutex
	accounts map[string]int64
	tokens   map[string]int64
	included map[string]bool
	events   []string
	// userPercent is every contract's consume_user_resource_percent; originEnergy is
	// the energy its deployer has left.
	userPercent  int64
	originEnergy int64
	// onInclude runs after each included transaction moved its TRX.
	onInclude func(f *fakeTronNode)
}

func newFakeTronNode(t *testing.T) *fakeTronNode {
	return &fakeTronNode{t: t, accounts: map[string]int64{}, tokens: map[string]int64{}, included: map[string]bool{},
		userPercent: tronTestFullUserPercent}
}

func tronHex(t *testing.T, address string) string {
	t.Helper()
	value, err := addressing.TronAddressToHex(address)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f *fakeTronNode) fund(t *testing.T, address string, sun int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[tronHex(t, address)] = sun
}

func (f *fakeTronNode) holdUSDT(t *testing.T, address string, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[tronHex(t, address)] = amount
}

func (f *fakeTronNode) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeTronNode) include(txID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.included[txID] = true
}

func (f *fakeTronNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	holder, _ := req["owner_address"].(string)
	if parameter, ok := req["parameter"].(string); ok {
		wantWords := 2
		if req["function_selector"] == "balanceOf(address)" {
			wantWords = 1
		}
		if decoded, err := hex.DecodeString(parameter); err != nil || len(decoded) != 32*wantWords {
			f.t.Errorf("fake TRON node: parameter %q is not %d ABI words", parameter, wantWords)
		}
	}
	switch r.URL.Path {
	case "/wallet/getchainparameters":
		reply(map[string]any{"chainParameter": []map[string]any{
			{"key": "getTransactionFee", "value": 1_000}, {"key": "getEnergyFee", "value": 100},
			{"key": "getCreateAccountFee", "value": 100_000}, {"key": "getCreateNewAccountFeeInSystemContract", "value": 1_000_000},
			{"key": "getMaxFeeLimit", "value": 15_000_000_000},
		}})
	case "/wallet/getaccount":
		address, _ := req["address"].(string)
		balance, ok := f.accounts[address]
		if !ok {
			_, _ = io.WriteString(w, "{}")
			return
		}
		reply(map[string]any{"address": address, "balance": balance})
	case "/wallet/getblock":
		reply(map[string]any{"blockID": tronTestHeadBlockID, "block_header": map[string]any{"raw_data": map[string]any{"number": 71532773, "timestamp": 1791126834000}}})
	case "/wallet/triggerconstantcontract":
		if req["function_selector"] == "balanceOf(address)" {
			word := make([]byte, 32)
			big.NewInt(f.tokens[holder]).FillBytes(word)
			reply(map[string]any{"result": map[string]any{"result": true}, "constant_result": []string{hex.EncodeToString(word)}})
			return
		}
		reply(map[string]any{"result": map[string]any{"result": true}, "energy_used": 14_650})
	case "/wallet/estimateenergy":
		if f.tokens[holder] == 0 {
			reply(map[string]any{"result": map[string]any{"code": "CONTRACT_EXE_ERROR", "message": hex.EncodeToString([]byte("REVERT opcode executed"))}})
			return
		}
		reply(map[string]any{"result": map[string]any{"result": true}, "energy_required": tronTestUSDTEnergy})
	case "/wallet/getcontract":
		reply(map[string]any{"origin_address": tronTestOriginHex, "consume_user_resource_percent": f.userPercent,
			"origin_energy_limit": tronTestOriginEnergyLimit})
	case "/wallet/getaccountresource":
		reply(map[string]any{"EnergyLimit": f.originEnergy})
	case "/wallet/gettransactioninfobyid":
		id, _ := req["value"].(string)
		f.events = append(f.events, "await:"+id)
		if !f.included[id] {
			_, _ = io.WriteString(w, "{}")
			return
		}
		reply(map[string]any{"id": id, "blockNumber": tronTestIncludedBlock, "receipt": map[string]any{}})
	default:
		f.t.Errorf("fake TRON node: unexpected call %s", r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}
}

// moveTRX applies a TransferContract's amount to the in-memory balances; other
// contracts leave them alone.
func (f *fakeTronNode) moveTRX(transaction []byte) {
	contract := protoBytes(protoBytes(transaction, tronPBRawData), tronPBContract)
	transfer := protoBytes(protoBytes(contract, tronPBParameter), tronPBAnyValue)
	from, to := protoBytes(transfer, tronPBTransferFrom), protoBytes(transfer, tronPBTransferTo)
	sun, ok := protoVarint(transfer, tronPBTransferSun)
	f.mu.Lock()
	defer f.mu.Unlock()
	if ok && len(from) > 0 && len(to) > 0 {
		f.accounts[hex.EncodeToString(from)] -= int64(sun)
		f.accounts[hex.EncodeToString(to)] += int64(sun)
	}
	if f.onInclude != nil {
		f.onInclude(f)
	}
}

// protoBytes is the first length-delimited field number of a protobuf message.
func protoBytes(message []byte, number protowire.Number) []byte {
	for len(message) > 0 {
		num, wireType, n := protowire.ConsumeTag(message)
		if n < 0 {
			return nil
		}
		message = message[n:]
		m := protowire.ConsumeFieldValue(num, wireType, message)
		if m < 0 {
			return nil
		}
		if num == number && wireType == protowire.BytesType {
			value, _ := protowire.ConsumeBytes(message)
			return value
		}
		message = message[m:]
	}
	return nil
}

// protoVarint is the first varint field number of a protobuf message.
func protoVarint(message []byte, number protowire.Number) (uint64, bool) {
	for len(message) > 0 {
		num, wireType, n := protowire.ConsumeTag(message)
		if n < 0 {
			return 0, false
		}
		message = message[n:]
		m := protowire.ConsumeFieldValue(num, wireType, message)
		if m < 0 {
			return 0, false
		}
		if num == number && wireType == protowire.VarintType {
			value, _ := protowire.ConsumeVarint(message)
			return value, true
		}
		message = message[m:]
	}
	return 0, false
}

// newTronSigningChain is the production Nile adapter against the fake node;
// broadcasts are recorded and immediately included, never sent.
func newTronSigningChain(t *testing.T, broadcasts *[]*types.SignedTx) (*chainfixtures.TronNile, *fakeTronNode) {
	t.Helper()
	node := newFakeTronNode(t)
	server := httptest.NewServer(node)
	t.Cleanup(server.Close)
	adapter := chainfixtures.NewTronNile(server.URL, []types.Token{tronTestUSDT}, func(_ context.Context, signed *types.SignedTx) (string, error) {
		*broadcasts = append(*broadcasts, signed)
		node.record("broadcast:" + signed.TxHash)
		node.include(signed.TxHash)
		node.moveTRX(signed.RawBytes)
		return signed.TxHash, nil
	})
	return adapter, node
}

// assertTronSentFrom decodes the Transaction protobuf and recovers its signer over
// sha256(raw_data) with go-ethereum alone, as java-tron does.
func assertTronSentFrom(t *testing.T, signed *types.SignedTx, address string) {
	t.Helper()
	var raw, signature []byte
	for data := signed.RawBytes; len(data) > 0; {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 || wireType != protowire.BytesType {
			t.Fatalf("unexpected transaction field %d/%d", number, wireType)
		}
		data = data[n:]
		value, m := protowire.ConsumeBytes(data)
		if m < 0 {
			t.Fatal("truncated transaction")
		}
		data = data[m:]
		switch number {
		case 1:
			raw = value
		case 2:
			if signature != nil {
				t.Fatal("more than one signature")
			}
			signature = value
		}
	}
	txID := sha256.Sum256(raw)
	if hex.EncodeToString(txID[:]) != signed.TxHash {
		t.Fatalf("tx hash %s is not sha256(raw_data)", signed.TxHash)
	}
	if len(signature) != 65 || signature[64] > 1 {
		t.Fatalf("signature %x is not r||s||recovery id", signature)
	}
	publicKey, err := crypto.SigToPub(txID[:], signature)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := addressing.EncodeTronAddress(append([]byte{addressing.TronAddressPrefix}, crypto.PubkeyToAddress(*publicKey).Bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	if signer != address {
		t.Fatalf("transaction recovers to %s, want %s", signer, address)
	}
}

func TestExecutePlan_TronBaseSignsTRXAsTheWalletAddress(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, node := newTronSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, tronTestChildIndex)
	base := *fixture.wallet.DepositAddress
	if !addressing.IsTronAddress(base.Address) || !addressing.IsTronAddress(fixture.child.Address) {
		t.Fatalf("addresses %s / %s", base.Address, fixture.child.Address)
	}
	node.fund(t, base.Address, 10_000_000)
	node.fund(t, tronTestDestination, 1)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTron, Asset: models.NativeTRX,
		Amount: big.NewInt(1_000_000), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	if _, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), tronTestDestination, "user-3119"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertTronSentFrom(t, broadcasts[0], base.Address)
}

func TestConsolidate_TronTRC20SweepSeedsTheChildAndWaitsBeforeSweeping(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, node := newTronSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, tronTestChildIndex)
	base, child := *fixture.wallet.DepositAddress, fixture.child
	node.fund(t, base.Address, 100_000_000)
	node.holdUSDT(t, child.Address, 7_000_000)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTron, Asset: models.SymbolUSDT}
	svc := fixture.executor(adapter)
	svc.registry.(*chain.Registry).RegisterToken(tronTestUSDT)

	leg, err := svc.broadcastLeg(context.Background(), adapter, mpcpkg.CurveSecp256k1,
		walletKeys{shareA: fixture.shareA, shareB: fixture.shareB}, fixture.wallet, plan,
		PlannedSweep{From: child, Amount: big.NewInt(7_000_000), NeedsGas: true}, uuid.New(),
		legBroadcastOpts{Origin: models.TxOriginManualConsolidation})
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 2 {
		t.Fatalf("broadcasts %d, want gas_seed + sweep", len(broadcasts))
	}
	seed, sweep := broadcasts[0], broadcasts[1]
	assertTronSentFrom(t, seed, base.Address)
	assertTronSentFrom(t, sweep, child.Address)
	if leg.TxHash != sweep.TxHash || leg.Amount == nil || leg.Amount.Int64() != 7_000_000 {
		t.Fatalf("leg %s / %v is not the sweep of 7 USDT", leg.TxHash, leg.Amount)
	}
	want := []string{"broadcast:" + seed.TxHash, "await:" + seed.TxHash, "broadcast:" + sweep.TxHash}
	if strings.Join(node.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v, want %v", node.events, want)
	}
	rows := svc.txRepo.(*fakeTxRepo).created
	if len(rows) != 2 || rows[0].TxType != models.TxTypeGasSeed || rows[0].Amount != "2982000" ||
		rows[1].TxType != models.TxTypeSweep || rows[1].TokenContract != models.USDTContractTronNile {
		t.Fatalf("rows %+v / %+v", rows[0], rows[1])
	}
}

func TestConsolidate_TronGasSeedCoversTheEstimateAndReseedsTheDelta(t *testing.T) {
	cases := []struct {
		name string
		// deployerRunsOut empties the deployer's energy once the first seed lands.
		deployerRunsOut bool
		wantSeeds       []string
	}{
		// Nile USDT: the deployer pays the energy, the child only 345 000 bandwidth × 120 %.
		{name: "deployer pays the energy", wantSeeds: []string{"414000"}},
		// The child now pays all the energy: it lacks 2 982 000 − 414 000.
		{name: "deployer runs out before the sweep", deployerRunsOut: true, wantSeeds: []string{"414000", "2568000"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var broadcasts []*types.SignedTx
			adapter, node := newTronSigningChain(t, &broadcasts)
			fixture := newSecp256k1WalletFixture(t, adapter, tronTestChildIndex)
			base, child := *fixture.wallet.DepositAddress, fixture.child
			node.fund(t, base.Address, 100_000_000)
			node.holdUSDT(t, child.Address, 7_000_000)
			node.userPercent, node.originEnergy = 0, 9_910_651
			if tc.deployerRunsOut {
				node.onInclude = func(f *fakeTronNode) { f.originEnergy = 0 }
			}
			plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTron, Asset: models.SymbolUSDT}
			svc := fixture.executor(adapter)
			svc.registry.(*chain.Registry).RegisterToken(tronTestUSDT)

			leg, err := svc.broadcastLeg(context.Background(), adapter, mpcpkg.CurveSecp256k1,
				walletKeys{shareA: fixture.shareA, shareB: fixture.shareB}, fixture.wallet, plan,
				PlannedSweep{From: child, Amount: big.NewInt(7_000_000), NeedsGas: true}, uuid.New(),
				legBroadcastOpts{Origin: models.TxOriginManualConsolidation})
			if err != nil {
				t.Fatal(err)
			}
			rows := svc.txRepo.(*fakeTxRepo).created
			if len(rows) != len(tc.wantSeeds)+1 || len(broadcasts) != len(rows) {
				t.Fatalf("%d rows, %d broadcasts, want %d gas_seeds + sweep", len(rows), len(broadcasts), len(tc.wantSeeds))
			}
			var events []string
			for i, want := range tc.wantSeeds {
				if rows[i].TxType != models.TxTypeGasSeed || rows[i].Amount != want || rows[i].FromAddress != base.Address || rows[i].ToAddress != child.Address {
					t.Fatalf("seed %d: %+v, want %s sun base → child", i, rows[i], want)
				}
				assertTronSentFrom(t, broadcasts[i], base.Address)
				events = append(events, "broadcast:"+broadcasts[i].TxHash, "await:"+broadcasts[i].TxHash)
			}
			sweep := rows[len(rows)-1]
			if sweep.TxType != models.TxTypeSweep || sweep.TxHash != leg.TxHash {
				t.Fatalf("last row %+v, want the sweep %s", sweep, leg.TxHash)
			}
			assertTronSentFrom(t, broadcasts[len(broadcasts)-1], child.Address)
			events = append(events, "broadcast:"+leg.TxHash)
			if strings.Join(node.events, ",") != strings.Join(events, ",") {
				t.Fatalf("events %v, want %v", node.events, events)
			}
		})
	}
}

func TestConsolidate_TronSweepWaitsWhenTheChildStaysShort(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, node := newTronSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, tronTestChildIndex)
	base, child := *fixture.wallet.DepositAddress, fixture.child
	node.fund(t, base.Address, 100_000_000)
	node.holdUSDT(t, child.Address, 7_000_000)
	node.userPercent, node.originEnergy = 0, 9_910_651
	childHex := tronHex(t, child.Address)
	// Something drains the child after every seed.
	node.onInclude = func(f *fakeTronNode) { f.originEnergy = 0; f.accounts[childHex] = 0 }
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTron, Asset: models.SymbolUSDT}
	svc := fixture.executor(adapter)
	svc.registry.(*chain.Registry).RegisterToken(tronTestUSDT)

	_, err := svc.broadcastLeg(context.Background(), adapter, mpcpkg.CurveSecp256k1,
		walletKeys{shareA: fixture.shareA, shareB: fixture.shareB}, fixture.wallet, plan,
		PlannedSweep{From: child, Amount: big.NewInt(7_000_000), NeedsGas: true}, uuid.New(),
		legBroadcastOpts{Origin: models.TxOriginManualConsolidation})
	if err == nil || !strings.Contains(err.Error(), "still lacks") {
		t.Fatalf("err %v, want the sweep refused", err)
	}
	if want := 1 + sweepFundingTopUps; len(broadcasts) != want {
		t.Fatalf("%d broadcasts, want %d gas_seeds and no sweep", len(broadcasts), want)
	}
	for _, signed := range broadcasts {
		assertTronSentFrom(t, signed, base.Address)
	}
}

func TestExecutePlan_TronMultiSweepWaitsForTheSweepsBeforeTheWithdrawal(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, node := newTronSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, tronTestChildIndex)
	base, child := *fixture.wallet.DepositAddress, fixture.child
	node.fund(t, base.Address, 2_000_000)
	node.fund(t, child.Address, 5_000_000)
	node.fund(t, tronTestDestination, 1)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainTron, Asset: models.NativeTRX,
		Amount: big.NewInt(4_000_000), Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: child, Amount: big.NewInt(3_900_000), NeedsGas: true}}}

	result, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), tronTestDestination, "user-3119")
	if err != nil || result.FailedStep != nil {
		t.Fatalf("result %+v: %v", result, err)
	}
	if len(broadcasts) != 2 {
		t.Fatalf("broadcasts %d, want the native sweep (no gas_seed) and the withdrawal", len(broadcasts))
	}
	sweep, withdrawal := broadcasts[0], broadcasts[1]
	assertTronSentFrom(t, sweep, child.Address)
	assertTronSentFrom(t, withdrawal, base.Address)
	want := []string{"broadcast:" + sweep.TxHash, "await:" + sweep.TxHash, "broadcast:" + withdrawal.TxHash}
	if strings.Join(node.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v, want %v", node.events, want)
	}
}

// ---------------------------------------------------------------------------
// Fee quotes
// ---------------------------------------------------------------------------

type tronQuoteFixture struct {
	svc      *service
	node     *fakeTronNode
	walletID uuid.UUID
	base     models.Address
	child    models.Address
}

func newTronQuoteFixture(t *testing.T) tronQuoteFixture {
	t.Helper()
	var broadcasts []*types.SignedTx
	adapter, node := newTronSigningChain(t, &broadcasts)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(tronTestUSDT)
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"}
	child := models.Address{ID: uuid.New(), WalletID: walletID, Address: "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainTron, DepositAddress: &base,
		FeeMultiplier: numeric.NewNullDecimal(decimal.RequireFromString("1.5"))}
	return tronQuoteFixture{
		svc: &service{
			registry:    registry,
			walletRepo:  &fakeWalletRepo{wallet: wallet},
			addressRepo: &fakeAddressRepo{children: []models.Address{base, child}},
			chainRepo:   &fakeChainRepo{chain: &models.Chain{ID: models.ChainTron, AdapterType: models.AdapterTypeTron}},
		},
		node: node, walletID: walletID, base: base, child: child,
	}
}

func TestQuoteTronNative_ExistingRecipientPaysBandwidthAndIgnoresTheMultiplier(t *testing.T) {
	f := newTronQuoteFixture(t)
	f.node.fund(t, f.base.Address, 10_000_000)
	f.node.fund(t, tronTestDestination, 1)

	q := quote(t, f.svc, f.walletID, models.NativeTRX, 1_000_000, tronTestDestination)

	requireBigInt(t, "fee", q.Fee, tronTestTRXFeeExisting)
	if q.Strategy != StrategyDirectFromBase || q.Transfers != 1 || q.Tron == nil || q.Tron.BandwidthBytes != 267 || q.Tron.ActivationFee.Sign() != 0 {
		t.Fatalf("quote %+v details %+v", q, q.Tron)
	}
	if !q.FeeMultiplier.Equal(models.FeeMultiplierMin) {
		t.Fatalf("fee multiplier %s applied to TRON", q.FeeMultiplier)
	}
	requireBigInt(t, "minimum remaining", q.MinimumRemaining, 0)
}

func TestQuoteTronNative_ProbeRecipientPaysTheActivation(t *testing.T) {
	f := newTronQuoteFixture(t)
	f.node.fund(t, f.base.Address, 10_000_000)

	q := quote(t, f.svc, f.walletID, models.NativeTRX, 1_000_000, "")

	requireBigInt(t, "fee", q.Fee, tronTestActivationFee)
	if !q.RecipientIsProbe || q.Tron.BandwidthFee.Sign() != 0 {
		t.Fatalf("quote %+v details %+v", q, q.Tron)
	}
}

func TestQuoteTronTRC20_MultiSweepMatchesThePlannerGas(t *testing.T) {
	f := newTronQuoteFixture(t)
	f.node.fund(t, f.base.Address, 50_000_000)
	f.node.holdUSDT(t, f.base.Address, 3_000_000)
	f.node.holdUSDT(t, f.child.Address, 4_000_000)

	q := quote(t, f.svc, f.walletID, models.SymbolUSDT, 5_000_000, tronTestDestination)

	// gas_seed to the never-activated child (activation) + child sweep + the
	// withdrawal from base, simulated with the 3 USDT base holds today.
	requireBigInt(t, "fee", q.Fee, tronTestActivationFee+2*tronTestUSDTFee)
	if q.Strategy != StrategyMultiSweep || q.Transfers != 3 || q.Tron.Energy != 2*tronTestUSDTEnergy || q.Tron.EnergyIsReference {
		t.Fatalf("quote %+v details %+v", q, q.Tron)
	}
	plan, err := f.svc.PlanForWithdrawal(context.Background(), f.walletID, models.SymbolUSDT, big.NewInt(5_000_000), tronTestDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(q.Fee) != 0 {
		t.Fatalf("planner gas %v, quote %v", plan.EstimatedGas, q.Fee)
	}
	if len(plan.Sweeps) != 1 || !plan.Sweeps[0].NeedsGas {
		t.Fatalf("sweeps %+v", plan.Sweeps)
	}
}

func TestQuoteTronTRC20_UnfundedWalletUsesTheReferenceEnergy(t *testing.T) {
	f := newTronQuoteFixture(t)
	f.node.fund(t, f.base.Address, 50_000_000)

	q := quote(t, f.svc, f.walletID, models.SymbolUSDT, 5_000_000, "")

	if q.Basis != FeeBasisUnfundedDirect || q.AmountSpendable || !q.Tron.EnergyIsReference {
		t.Fatalf("quote %+v details %+v", q, q.Tron)
	}
	requireBigInt(t, "fee", q.Fee, tronTestReferenceUSDTFee)
}

func TestTronSweepLimitsAndSupport(t *testing.T) {
	if !supportedSweepAdapter(models.AdapterTypeTron) || !chainNeedsGasSeed(models.ChainTron) || localSignChain(models.ChainTron) {
		t.Fatal("TRON must sweep with gas seeds and MPC signing")
	}
	limits, _ := (&service{}).LoadLimits(context.Background(), uuid.Nil)
	if limits.MaxAddressesPerRequest[models.AdapterTypeTron] != 50 {
		t.Fatalf("TRON address cap %d", limits.MaxAddressesPerRequest[models.AdapterTypeTron])
	}
	if quoteRecipient(models.AdapterTypeTron, "") != chain.TronFeeProbeRecipient() || quoteRecipient(models.AdapterTypeTron, tronTestDestination) != tronTestDestination {
		t.Fatal("TRON quote recipient")
	}
}
