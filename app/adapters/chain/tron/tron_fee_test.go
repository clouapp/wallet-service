package tron

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// Bandwidth of what BuildTransfer writes (millisecond timestamps): a TRX transfer
	// with a 3-byte amount varint — the real Nile transfer 35bd7fdf… was charged 270
	// because its timestamp is in nanoseconds, 3 bytes wider — and a USDT transfer
	// with a 4-byte fee_limit varint, like the real c4c6e63a… (charged 345).
	tronTestTRXBandwidthBytes   = 267
	tronTestTRC20BandwidthBytes = 345
)

type tronFeeFixture struct {
	node    *fakeTronNode
	adapter *TronLive
	key     []byte
	sender  string
	other   string
}

func newTronFeeFixture(t *testing.T) tronFeeFixture {
	t.Helper()
	senderKey, sender := tronTestKey(t, tronTestSenderKeyHex)
	_, other := tronTestKey(t, tronTestOtherKeyHex)
	node := newFakeTronNode(t)
	return tronFeeFixture{node: node, adapter: newTronTestAdapter(t, node), key: crypto.FromECDSA(senderKey), sender: sender, other: other}
}

// requireBuiltMatchesQuote signs the built transaction and checks the quote priced
// exactly its bandwidth and fee_limit.
func (f tronFeeFixture) requireBuiltMatchesQuote(t *testing.T, unsigned *types.UnsignedTx, quote TronFeeQuote) {
	t.Helper()
	signed, err := f.adapter.SignTransaction(context.Background(), unsigned, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(len(signed.RawBytes)) + tronMaxResultSizeInTx; got != quote.BandwidthBytes {
		t.Fatalf("signed transaction charges %d bandwidth bytes, quote priced %d", got, quote.BandwidthBytes)
	}
	raw, err := decodeTronRawData(mustHex(t, unsigned.Metadata["raw_data_hex"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if quote.FeeLimit.Cmp(big.NewInt(raw.feeLimit)) != 0 || unsigned.Metadata["fee_limit"] != raw.feeLimit {
		t.Fatalf("fee_limit %d, quote %v", raw.feeLimit, quote.FeeLimit)
	}
	if unsigned.Metadata["fee"] != quote.Fee().String() {
		t.Fatalf("metadata fee %v, quote %v", unsigned.Metadata["fee"], quote.Fee())
	}
}

func TestTronFeeTRXToExistingAccount(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.fund(t, f.sender, 10_000_000)
	f.node.fund(t, f.other, 1)
	req := types.TransferRequest{From: f.sender, To: f.other, Amount: big.NewInt(1_234_567), Asset: "TRX"}

	quote, err := f.adapter.QuoteTransferFee(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if quote.BandwidthBytes != tronTestTRXBandwidthBytes {
		t.Fatalf("bandwidth = %d, want %d", quote.BandwidthBytes, tronTestTRXBandwidthBytes)
	}
	requireSun(t, "bandwidth fee", quote.BandwidthFee, 267_000)
	requireSun(t, "activation fee", quote.ActivationFee, 0)
	requireSun(t, "fee", quote.Fee(), 267_000)

	unsigned, err := f.adapter.BuildTransfer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	f.requireBuiltMatchesQuote(t, unsigned, quote)
	requireSun(t, "transfer amount", unsigned.TransferAmount, 1_234_567)
	if unsigned.Metadata["contract_type"] != tronTransferContractName || unsigned.Metadata["to"] != f.other {
		t.Fatalf("metadata %v", unsigned.Metadata)
	}
	raw, _ := decodeTronRawData(mustHex(t, unsigned.Metadata["raw_data_hex"].(string)))
	if raw.expiration != tronTestHeadTimestamp+tronTxExpirationWindow.Milliseconds() || raw.timestamp != tronTestNow {
		t.Fatalf("expiration %d timestamp %d", raw.expiration, raw.timestamp)
	}
	if raw.feeLimit != 0 || raw.contract.amount != 1_234_567 {
		t.Fatalf("raw %+v", raw)
	}
}

func TestTronFeeTRXToNewAccountPaysActivation(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.fund(t, f.sender, 10_000_000)
	quote, err := f.adapter.QuoteTransferFee(context.Background(), types.TransferRequest{From: f.sender, To: f.other, Amount: big.NewInt(1_234_567), Asset: "TRX"})
	if err != nil {
		t.Fatal(err)
	}
	// getCreateNewAccountFeeInSystemContract + getCreateAccountFee, no bandwidth
	// (the real activation 35ad1db7… burned exactly 1.1 TRX).
	requireSun(t, "activation fee", quote.ActivationFee, 1_100_000)
	requireSun(t, "bandwidth fee", quote.BandwidthFee, 0)
	requireSun(t, "fee", quote.Fee(), 1_100_000)
}

func TestTronNativeTransferReserveAndGasUnits(t *testing.T) {
	f := newTronFeeFixture(t)
	fee, minimum, err := f.adapter.NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	requireSun(t, "reserve", fee, 1_100_000)
	requireSun(t, "minimum remaining", minimum, 0)

	price, _ := f.adapter.EstimateGasPrice(context.Background())
	requireSun(t, "gas price", price, 1)
	units, err := f.adapter.NativeTransferGasLimit(context.Background(), "", "")
	if err != nil || units != 1_100_000 {
		t.Fatalf("native gas units %d: %v", units, err)
	}
	f.node.fund(t, f.other, 1)
	units, err = f.adapter.NativeTransferGasLimit(context.Background(), f.sender, f.other)
	// Unknown amount: sized with the widest (9-byte) varint, 6 bytes above 267.
	if err != nil || units != 273_000 {
		t.Fatalf("native gas units to existing account %d: %v", units, err)
	}
	fixed := uint64(42)
	units, err = f.adapter.EstimateTransferGasLimit(context.Background(), types.TransferRequest{GasLimit: &fixed})
	if err != nil || units != fixed {
		t.Fatalf("explicit gas limit: %d %v", units, err)
	}
	estimate, err := f.adapter.EstimateFee(context.Background(), types.TransferRequest{From: f.sender, To: f.other, Amount: big.NewInt(1_234_567)})
	if err != nil || estimate.Fee != "0.267" || estimate.FeeAsset != models.NativeTRX {
		t.Fatalf("EstimateFee = %+v, %v", estimate, err)
	}
}

func usdtRequest(from, to string, amount int64) types.TransferRequest {
	token := tronTestNileUSDT
	return types.TransferRequest{From: from, To: to, Amount: big.NewInt(amount), Asset: token.Symbol, Token: &token}
}

func TestTronFeeTRC20ExistingAndNewHolder(t *testing.T) {
	cases := []struct {
		name           string
		energyRequired int64
		wantFeeLimit   int64
	}{
		// estimateenergy on Nile today: USDT to an existing holder 21 975, to a new one 37 063.
		{"existing holder", 21_975, 2_637_000},
		{"new holder", 37_063, 4_447_560},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTronFeeFixture(t)
			f.node.fund(t, f.sender, 10_000_000)
			f.node.holdTokens(t, f.sender, 50_000_000)
			f.node.energyRequired = tc.energyRequired
			req := usdtRequest(f.sender, f.other, 5_000_000)

			quote, err := f.adapter.QuoteTransferFee(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if quote.Energy != tc.energyRequired || quote.EnergyIsReference || quote.BandwidthBytes != tronTestTRC20BandwidthBytes {
				t.Fatalf("quote %s", describeQuote(quote))
			}
			requireSun(t, "fee limit", quote.FeeLimit, tc.wantFeeLimit)
			requireSun(t, "fee", quote.Fee(), tc.wantFeeLimit+345_000)

			unsigned, err := f.adapter.BuildTransfer(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			f.requireBuiltMatchesQuote(t, unsigned, quote)
			if unsigned.Metadata["token_contract"] != tronTestNileUSDT.Contract || unsigned.Metadata["contract_type"] != tronTriggerSmartContractName {
				t.Fatalf("metadata %v", unsigned.Metadata)
			}
		})
	}
}

func TestTronFeeTRC20FallsBackToTriggerConstantContract(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.holdTokens(t, f.sender, 50_000_000)
	f.node.energyRequired = 0
	f.node.energyUsed = 64_285 // mainnet USDT to an existing holder, penalty included
	quote, err := f.adapter.QuoteTransferFee(context.Background(), usdtRequest(f.sender, f.other, 5_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if quote.Energy != 64_285 {
		t.Fatalf("energy %d", quote.Energy)
	}
	requireSun(t, "fee limit", quote.FeeLimit, 7_714_200)
	requireSun(t, "fee", quote.Fee(), 8_059_200)
}

func TestTronFeeTRC20SenderWithoutTokens(t *testing.T) {
	f := newTronFeeFixture(t)
	req := usdtRequest(f.sender, f.other, 5_000_000)
	quote, err := f.adapter.QuoteTransferFee(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !quote.EnergyIsReference || quote.Energy != tronTRC20ReferenceEnergy {
		t.Fatalf("quote %s", describeQuote(quote))
	}
	requireSun(t, "fee limit", quote.FeeLimit, 15_634_200)
	if _, err := f.adapter.BuildTransfer(context.Background(), req); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("build without tokens: %v", err)
	}
	if f.node.callCount("/wallet/estimateenergy") != 0 {
		t.Fatal("simulated a transfer the sender cannot make")
	}

	f.node.holdTokens(t, f.sender, 1_000)
	quote, err = f.adapter.QuoteTransferFee(context.Background(), req)
	if err != nil || quote.EnergyIsReference || quote.Energy != f.node.energyRequired {
		t.Fatalf("partial balance quote %s: %v", describeQuote(quote), err)
	}
	if _, err := f.adapter.BuildTransfer(context.Background(), req); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("build with a partial balance: %v", err)
	}
}

func TestTronFeeTRC20SimulationRevertFails(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.holdTokens(t, f.sender, 50_000_000)
	f.node.revertTransfer = true
	if _, err := f.adapter.QuoteTransferFee(context.Background(), usdtRequest(f.sender, f.other, 5_000_000)); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("reverting simulation: %v", err)
	}
}

func TestTronFeeLimitCeilings(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.holdTokens(t, f.sender, 50_000_000)
	// 1 250 001 energy × 100 sun × 120 % is just above the 150 TRX cap.
	f.node.energyRequired = 1_250_001
	if _, err := f.adapter.QuoteTransferFee(context.Background(), usdtRequest(f.sender, f.other, 5_000_000)); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("fee_limit above the cap: %v", err)
	}
	f.node.energyRequired = 1_250_000
	quote, err := f.adapter.QuoteTransferFee(context.Background(), usdtRequest(f.sender, f.other, 5_000_000))
	if err != nil {
		t.Fatal(err)
	}
	requireSun(t, "fee limit at the cap", quote.FeeLimit, tronFeeLimitCapSun)

	params := tronChainParams{sunPerBandwidthByte: 1_000, sunPerEnergy: 100, maxFeeLimit: 2_000_000}
	if _, err := tronEnergyFeeLimit(21_975, params); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("fee_limit above getMaxFeeLimit: %v", err)
	}
	if _, err := tronEnergyFeeLimit(0, params); !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("zero energy: %v", err)
	}
}

func TestTronChainParamsCacheAndValidation(t *testing.T) {
	f := newTronFeeFixture(t)
	f.node.fund(t, f.other, 1)
	clock := time.UnixMilli(tronTestNow)
	f.adapter.now = func() time.Time { return clock }
	req := types.TransferRequest{From: f.sender, To: f.other, Amount: big.NewInt(1)}
	for range 3 {
		if _, err := f.adapter.QuoteTransferFee(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if calls := f.node.callCount("/wallet/getchainparameters"); calls != 1 {
		t.Fatalf("getchainparameters called %d times within the TTL", calls)
	}
	clock = clock.Add(tronChainParamsTTL)
	if _, err := f.adapter.QuoteTransferFee(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if calls := f.node.callCount("/wallet/getchainparameters"); calls != 2 {
		t.Fatalf("getchainparameters called %d times after the TTL", calls)
	}

	broken := newTronFeeFixture(t)
	delete(broken.node.params, tronParamEnergyFee)
	if _, err := broken.adapter.QuoteTransferFee(context.Background(), req); err == nil {
		t.Fatal("missing getEnergyFee accepted")
	}
}

func TestTronBuildSweepTRC20WithGasSeed(t *testing.T) {
	f := newTronFeeFixture(t)
	base, child := f.other, f.sender
	f.node.fund(t, base, 100_000_000)
	f.node.holdTokens(t, child, 7_000_000)
	token := tronTestNileUSDT

	txs, err := f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Token: &token, Asset: token.Symbol, NativeBalance: new(big.Int)})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("got %d transactions, want gas_seed + sweep", len(txs))
	}
	seed, sweep := txs[0], txs[1]
	// The sender pays all the energy: (345 000 + 21 975 × 100) × 120 % = 3 051 000,
	// capped at the sweep's own ceiling 345 000 + fee_limit 2 637 000. The seed
	// activates the child, so it costs 1.1 TRX itself.
	requireSun(t, "gas seed amount", seed.TransferAmount, 2_982_000)
	if seed.Metadata["owner_address"] != base || seed.Metadata["to"] != child || seed.Metadata["fee"] != "1100000" {
		t.Fatalf("seed metadata %v", seed.Metadata)
	}
	if sweep.Metadata["owner_address"] != child || sweep.Metadata["to"] != base || sweep.Metadata["fee"] != "2982000" {
		t.Fatalf("sweep metadata %v", sweep.Metadata)
	}
	requireSun(t, "sweep amount", sweep.TransferAmount, 7_000_000)

	txs, err = f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Token: &token, Asset: token.Symbol, NativeBalance: big.NewInt(2_982_000)})
	if err != nil || len(txs) != 1 {
		t.Fatalf("child with enough TRX: %d txs, %v", len(txs), err)
	}
}

func TestTronGasSeedCoversOnlyTheEnergyTheChildPays(t *testing.T) {
	token := tronTestNileUSDT
	cases := []struct {
		name        string
		userPercent int64
		originLeft  int64
		held        int64
		wantSeed    int64
	}{
		// Nile USDT: consume_user_resource_percent 0, the deployer has energy to
		// spare, so the child pays bandwidth only: 345 000 × 120 %.
		{name: "deployer pays all the energy", userPercent: 0, originLeft: 9_910_651, wantSeed: 414_000},
		{name: "a funded child gets the delta", userPercent: 0, originLeft: 9_910_651, held: 300_000, wantSeed: 114_000},
		{name: "a child holding enough gets none", userPercent: 0, originLeft: 9_910_651, held: 414_000},
		// 70 % of 21 975 = 15 382 from the deployer; (345 000 + 6 593 × 100) × 120 %.
		{name: "split energy", userPercent: 30, originLeft: 9_910_651, wantSeed: 1_205_160},
		// Mainnet USDT: the deployer's energy is used up, so the caller pays all.
		{name: "deployer out of energy", userPercent: 30, originLeft: 0, wantSeed: 2_982_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTronFeeFixture(t)
			base, child := f.other, f.sender
			f.node.fund(t, base, 100_000_000)
			f.node.holdTokens(t, child, 7_000_000)
			f.node.contractUserPercent, f.node.originEnergyLeft = tc.userPercent, tc.originLeft

			txs, err := f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Token: &token,
				Asset: token.Symbol, NativeBalance: big.NewInt(tc.held)})
			if err != nil {
				t.Fatal(err)
			}
			sweep := txs[len(txs)-1]
			if sweep.Metadata["fee"] != "2982000" {
				t.Fatalf("sweep fee %v: the transaction keeps its full fee_limit", sweep.Metadata["fee"])
			}
			if tc.wantSeed == 0 {
				if len(txs) != 1 {
					t.Fatalf("got %d transactions, want the sweep alone", len(txs))
				}
				return
			}
			if len(txs) != 2 {
				t.Fatalf("got %d transactions, want gas_seed + sweep", len(txs))
			}
			requireSun(t, "gas seed amount", txs[0].TransferAmount, tc.wantSeed)
		})
	}
}

func TestTronSweepFundingShortfallRepricesBeforeTheSweep(t *testing.T) {
	f := newTronFeeFixture(t)
	base, child := f.other, f.sender
	f.node.fund(t, base, 100_000_000)
	f.node.holdTokens(t, child, 7_000_000)
	f.node.contractUserPercent, f.node.originEnergyLeft = 0, 9_910_651
	token := tronTestNileUSDT

	txs, err := f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Token: &token,
		Asset: token.Symbol, NativeBalance: new(big.Int)})
	if err != nil || len(txs) != 2 {
		t.Fatalf("%d txs, %v", len(txs), err)
	}
	sweep := &txs[1]
	f.node.fund(t, child, 414_000)
	shortfall, err := f.adapter.SweepFundingShortfall(context.Background(), sweep)
	if err != nil {
		t.Fatal(err)
	}
	requireSun(t, "shortfall once seeded", shortfall, 0)

	// The deployer ran out of energy between the seed and the sweep: the child now
	// pays all of it and lacks 2 982 000 − 414 000.
	f.node.originEnergyLeft = 0
	shortfall, err = f.adapter.SweepFundingShortfall(context.Background(), sweep)
	if err != nil {
		t.Fatal(err)
	}
	requireSun(t, "shortfall after the deployer ran out", shortfall, 2_568_000)

	f.node.failPaths["/wallet/getcontract"] = http.StatusBadGateway
	f.node.originEnergyLeft = 9_910_651
	shortfall, err = f.adapter.SweepFundingShortfall(context.Background(), sweep)
	if err != nil {
		t.Fatal(err)
	}
	requireSun(t, "shortfall when the split is unreadable", shortfall, 2_568_000)

	native, err := f.adapter.SweepFundingShortfall(context.Background(), &types.UnsignedTx{Metadata: map[string]interface{}{"owner_address": child}})
	if err != nil || native.Sign() != 0 {
		t.Fatalf("TRX sweep shortfall %v, %v", native, err)
	}
}

func TestTronBuildSweepNative(t *testing.T) {
	f := newTronFeeFixture(t)
	base, child := f.other, f.sender
	f.node.fund(t, base, 1)
	f.node.fund(t, child, 5_000_000)
	txs, err := f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Asset: "TRX", NativeBalance: big.NewInt(5_000_000)})
	if err != nil || len(txs) != 1 {
		t.Fatalf("native sweep: %d txs, %v", len(txs), err)
	}
	// 5 000 000 sun is a 4-byte varint: 268 bandwidth bytes.
	requireSun(t, "swept", txs[0].TransferAmount, 5_000_000-268_000)

	if _, err := f.adapter.BuildSweep(context.Background(), types.SweepRequest{From: child, To: base, Asset: "TRX", NativeBalance: big.NewInt(267_000)}); err == nil {
		t.Fatal("swept a balance that only covers the fee")
	}
}
