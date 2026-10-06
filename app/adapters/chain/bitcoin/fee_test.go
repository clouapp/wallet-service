package bitcoin

import (
	"context"
	"fmt"
	"math/big"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

// liveTestnet4FeeEstimates is a real mempool.space testnet4 /fee-estimates answer.
const liveTestnet4FeeEstimates = `{"1":1,"2":1,"3":0.5,"4":0.5,"5":0.5,"6":0.1,"7":0.1,"8":0.1,"9":0.1,"10":0.1,"11":0.1,"12":0.1,"13":0.1,"14":0.1,"15":0.1,"16":0.1,"17":0.1,"18":0.1,"19":0.1,"20":0.1,"21":0.1,"22":0.1,"23":0.1,"24":0.1,"25":0.1,"144":0.1,"504":0.1,"1008":0.1}`

const (
	feeTestFrom = "tb1qhq3t9kaa3qll3kv7jt8m6yxmuqkmpg0a8zmmcd"
	feeTestTo   = "tb1qur7330emxypadqvr0mu4989sfzw32gpkxgyd2m"
	flatTestFee = int64(btcFeeVBytes * btcDefaultFeeRate)
)

func utxo(i int, value int64) btcInput {
	return btcInput{TxID: fmt.Sprintf("%064x", i+1), Vout: uint32(i), Value: value, Address: feeTestFrom}
}

func utxoJSON(inputs []btcInput, confirmed bool) string {
	body := "["
	for i, in := range inputs {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"txid":"%s","vout":%d,"value":%d,"status":{"confirmed":%t}}`, in.TxID, in.Vout, in.Value, confirmed)
	}
	return body + "]"
}

func rate(satPerVByte int64) btcFeePolicy {
	return btcFeePolicy{milliSatPerVByte: satPerVByte * milliSatsPerSat, flatFee: flatTestFee}
}

func TestP2_WPKHV_SizeRoundsUp(t *testing.T) {
	cases := []struct {
		inputs, outputs int
		want            int64
	}{
		{1, 1, 110}, // 109.5
		{1, 2, 141}, // 140.5
		{2, 2, 209}, // 208.5
		{3, 1, 246}, // 245.5
		{0, 0, 11},
	}
	for _, tc := range cases {
		if got := p2wpkhVSize(tc.inputs, tc.outputs); got != tc.want {
			t.Errorf("vsize(%d in, %d out) = %d, want %d", tc.inputs, tc.outputs, got, tc.want)
		}
	}
}

func TestFee_Policy_FeeIsCeilRateTimesVSizeWithARelayFloor(t *testing.T) {
	cases := []struct {
		name            string
		policy          btcFeePolicy
		inputs, outputs int
		want            int64
	}{
		{"1 sat/vB", rate(1), 1, 2, 141},
		{"fractional rate rounds the fee up", btcFeePolicy{milliSatPerVByte: 2500}, 1, 2, 353},
		{"12.3 sat/vB, 2 inputs", btcFeePolicy{milliSatPerVByte: 12300}, 2, 2, 2571},
		{"sub-relay rate is lifted to 1 sat/vB", btcFeePolicy{milliSatPerVByte: 100}, 1, 2, 141},
		{"flat fallback", btcFeePolicy{flatFee: flatTestFee}, 1, 2, flatTestFee},
		{"flat fallback below relay for many inputs", btcFeePolicy{flatFee: flatTestFee}, 30, 2, p2wpkhVSize(30, 2)},
	}
	for _, tc := range cases {
		if got := tc.policy.fee(tc.inputs, tc.outputs); got != tc.want {
			t.Errorf("%s: fee = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestParse_Esplora_FeeEstimates(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    int64
		wantErr bool
	}{
		{"live testnet4 answer: 0.5 sat/vB floored to relay", liveTestnet4FeeEstimates, btcMinRelayMilliSatPerVByte, false},
		{"3-block target wins", `{"1":40,"3":12.3,"6":5}`, 12300, false},
		{"falls back to the next target up to 6", `{"1":40,"2":30,"5":7.25}`, 7250, false},
		{"no 3-6 block target", `{"1":40,"144":1}`, 0, true},
		{"zero rate", `{"3":0}`, 0, true},
		{"negative rate", `{"3":-2}`, 0, true},
		{"absurd rate", `{"3":1e9}`, 0, true},
		{"quoted decimal rate", `{"3":"12"}`, 12000, false},
		{"quoted garbage", `{"3":"soon"}`, 0, true},
		{"not an object", `[1,2,3]`, 0, true},
		{"html", `<html>`, 0, true},
		{"empty", ``, 0, true},
	}
	for _, tc := range cases {
		got, err := parseEsploraFeeEstimates([]byte(tc.body))
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: rate %d err %v, want %d (error %t)", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestSelect_BTC_Spend(t *testing.T) {
	policy := rate(2)
	oneInChange := policy.fee(1, 2)   // 282
	oneInNoChange := policy.fee(1, 1) // 220

	t.Run("change at least dust goes back to the sender", func(t *testing.T) {
		spend, err := selectBTCSpend([]btcInput{utxo(0, 100_000)}, 50_000, policy)
		if err != nil || spend.fee != oneInChange || spend.change != 100_000-50_000-oneInChange {
			t.Fatalf("spend %+v err %v", spend, err)
		}
	})
	t.Run("change below dust is dropped into the fee", func(t *testing.T) {
		amount := 100_000 - oneInChange - (btcDustSats - 1)
		spend, err := selectBTCSpend([]btcInput{utxo(0, 100_000)}, amount, policy)
		if err != nil || spend.change != 0 || spend.fee != 100_000-amount || spend.fee < oneInNoChange {
			t.Fatalf("spend %+v err %v", spend, err)
		}
	})
	t.Run("change of exactly dust is kept", func(t *testing.T) {
		amount := 100_000 - oneInChange - btcDustSats
		spend, err := selectBTCSpend([]btcInput{utxo(0, 100_000)}, amount, policy)
		if err != nil || spend.change != btcDustSats {
			t.Fatalf("spend %+v err %v", spend, err)
		}
	})
	t.Run("each extra input raises the fee", func(t *testing.T) {
		// Two inputs (60000) leave 300 for a fee of at least 356 (178 vB × 2): a third is needed.
		utxos := []btcInput{utxo(0, 30_000), utxo(1, 30_000), utxo(2, 30_000)}
		spend, err := selectBTCSpend(utxos, 59_700, policy)
		if err != nil || len(spend.inputs) != 3 || spend.fee != policy.fee(3, 2) || spend.change != 90_000-59_700-policy.fee(3, 2) {
			t.Fatalf("spend %+v err %v", spend, err)
		}
	})
	t.Run("largest utxos first", func(t *testing.T) {
		utxos := []btcInput{utxo(0, 1_000), utxo(1, 90_000), utxo(2, 5_000)}
		spend, err := selectBTCSpend(utxos, 50_000, policy)
		if err != nil || len(spend.inputs) != 1 || spend.inputs[0].Value != 90_000 {
			t.Fatalf("spend %+v err %v", spend, err)
		}
	})
	t.Run("insufficient", func(t *testing.T) {
		if _, err := selectBTCSpend([]btcInput{utxo(0, 10_000)}, 10_000-oneInNoChange+1, policy); err == nil {
			t.Fatal("expected insufficient funds")
		}
		if _, err := selectBTCSpend(nil, 1_000, policy); err == nil {
			t.Fatal("expected insufficient funds without utxos")
		}
	})
	t.Run("amount below dust", func(t *testing.T) {
		if _, err := selectBTCSpend([]btcInput{utxo(0, 100_000)}, btcDustSats-1, policy); err == nil {
			t.Fatal("expected a dust error")
		}
	})
	t.Run("does not reorder the caller's slice", func(t *testing.T) {
		utxos := []btcInput{utxo(0, 1_000), utxo(1, 90_000)}
		if _, err := selectBTCSpend(utxos, 50_000, policy); err != nil {
			t.Fatal(err)
		}
		if utxos[0].Value != 1_000 {
			t.Fatal("caller's utxos were mutated")
		}
	})
}

// Every amount up to maxSendableSats must build, balance exactly, and pay at least
// the min relay fee: the planner promises exactly that.
func TestSelect_BTC_SpendAlwaysSucceedsUpToMaxSendable(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for trial := 0; trial < 500; trial++ {
		policy := btcFeePolicy{milliSatPerVByte: int64(rng.IntN(50_000)) + 100, flatFee: flatTestFee}
		if trial%5 == 0 {
			policy.milliSatPerVByte = 0
		}
		utxos := make([]btcInput, rng.IntN(12)+1)
		for i := range utxos {
			utxos[i] = utxo(i, int64(rng.IntN(200_000))+1)
		}
		maxSendable := maxSendableSats(utxos, policy)
		if maxSendable == 0 {
			continue
		}
		for _, amount := range []int64{maxSendable, btcDustSats, btcDustSats + rng.Int64N(maxSendable-btcDustSats+1)} {
			spend, err := selectBTCSpend(utxos, amount, policy)
			if err != nil {
				t.Fatalf("trial %d: amount %d ≤ max %d failed: %v", trial, amount, maxSendable, err)
			}
			outputs := spend.outputs(feeTestFrom, feeTestTo, amount)
			var paid int64
			for _, out := range outputs {
				paid += out.Value
			}
			if paid+spend.fee != sumBTCInputs(spend.inputs) {
				t.Fatalf("trial %d: outputs %d + fee %d != inputs %d", trial, paid, spend.fee, sumBTCInputs(spend.inputs))
			}
			if spend.change != 0 && spend.change < btcDustSats {
				t.Fatalf("trial %d: change %d below dust", trial, spend.change)
			}
			if minFee := p2wpkhVSize(len(spend.inputs), len(outputs)); spend.fee < minFee {
				t.Fatalf("trial %d: fee %d below relay %d", trial, spend.fee, minFee)
			}
		}
	}
}

func TestMax_Sendable_Sats(t *testing.T) {
	policy := rate(1)
	utxos := []btcInput{utxo(0, 10_000), utxo(1, 5_000), utxo(2, 0)}
	if got, want := maxSendableSats(utxos, policy), int64(15_000)-policy.fee(2, 1); got != want {
		t.Fatalf("max %d, want %d", got, want)
	}
	if got := maxSendableSats([]btcInput{utxo(0, policy.fee(1, 1)+btcDustSats-1)}, policy); got != 0 {
		t.Fatalf("leftover below dust must be 0, got %d", got)
	}
	if got := maxSendableSats(nil, policy); got != 0 {
		t.Fatalf("no utxos: %d", got)
	}
}

func feeTestAdapter(esplora *fakeEsplora) *BitcoinLive { return esplora.adapter(nil) }

func utxoPath() string { return esploraTestPrefix + "/address/" + feeTestFrom + "/utxo" }

func TestBuild_Transfer_UsesTheEstimatedRateAndCountsInputs(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":4.2,"6":2}`)
	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 40_000), utxo(1, 40_000), utxo(2, 40_000)}, true))
	live := feeTestAdapter(esplora)

	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(70_000)})
	if err != nil {
		t.Fatal(err)
	}

	inputs := inputsFrom(unsigned)
	outputs, _ := outputsFrom(unsigned)
	wantFee := ceilDiv(p2wpkhVSize(2, 2)*4200, milliSatsPerSat)
	if len(inputs) != 2 || len(outputs) != 2 || unsigned.Metadata["fee"] != wantFee {
		t.Fatalf("inputs %d outputs %d fee %v, want 2/2/%d", len(inputs), len(outputs), unsigned.Metadata["fee"], wantFee)
	}
	if outputs[0].Address != feeTestTo || outputs[0].Value != 70_000 || outputs[1].Address != feeTestFrom || outputs[1].Value != 80_000-70_000-wantFee {
		t.Fatalf("outputs %+v", outputs)
	}
}

func TestBuild_Transfer_FallsBackToTheFlatFeeWhenEstimatesFail(t *testing.T) {
	for name, answer := range map[string]esploraAnswer{
		"http 500":  {http.StatusInternalServerError, "boom"},
		"garbage":   {http.StatusOK, `{"3":"soon"}`},
		"no target": {http.StatusOK, `{"144":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			esplora := newFakeEsplora(t)
			esplora.on(esploraTestPrefix+"/fee-estimates", answer)
			esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 200_000)}, true))

			unsigned, err := feeTestAdapter(esplora).BuildTransfer(context.Background(), types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(100_000)})

			if err != nil || unsigned.Metadata["fee"] != flatTestFee {
				t.Fatalf("fee %v err %v, want flat %d", unsigned.Metadata["fee"], err, flatTestFee)
			}
		})
	}
}

func TestBuild_Transfer_SpendsOnlyConfirmedUTXOs(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":1}`)
	body := `[` +
		`{"txid":"` + utxo(0, 0).TxID + `","vout":0,"value":500000,"status":{"confirmed":false}},` +
		`{"txid":"` + utxo(1, 0).TxID + `","vout":1,"value":20000,"status":{"confirmed":true,"block_height":154740}}` +
		`]`
	esplora.ok(utxoPath(), body)
	live := feeTestAdapter(esplora)

	if _, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(100_000)}); err == nil {
		t.Fatal("unconfirmed utxos must not be spent")
	}
	unsigned, err := live.BuildTransfer(context.Background(), types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(10_000)})
	if err != nil || len(inputsFrom(unsigned)) != 1 || inputsFrom(unsigned)[0].Value != 20_000 {
		t.Fatalf("unsigned %+v err %v", unsigned, err)
	}
}

func TestBuild_Sweep_WithoutAmountSendsEverythingAfterTheFee(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":3}`)
	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 30_000), utxo(1, 20_000), utxo(2, 10_000)}, true))

	txs, err := feeTestAdapter(esplora).BuildSweep(context.Background(), types.SweepRequest{From: feeTestFrom, To: feeTestTo})
	if err != nil || len(txs) != 1 {
		t.Fatalf("txs %d err %v", len(txs), err)
	}
	outputs, _ := outputsFrom(&txs[0])
	wantFee := rate(3).fee(3, 1)
	if len(inputsFrom(&txs[0])) != 3 || len(outputs) != 1 || outputs[0].Value != 60_000-wantFee || txs[0].Metadata["fee"] != wantFee {
		t.Fatalf("outputs %+v fee %v", outputs, txs[0].Metadata["fee"])
	}
}

func TestBuild_Sweep_OfThePlannedAmountUsesEveryInput(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":3}`)
	utxos := []btcInput{utxo(0, 30_000), utxo(1, 20_000)}
	esplora.ok(utxoPath(), utxoJSON(utxos, true))
	live := feeTestAdapter(esplora)

	funds, err := live.SpendableFunds(context.Background(), feeTestFrom)
	if err != nil {
		t.Fatal(err)
	}
	sweepable := new(big.Int).Sub(funds.Balance, funds.MaxTransferFee)
	txs, err := live.BuildSweep(context.Background(), types.SweepRequest{From: feeTestFrom, To: feeTestTo, Amount: sweepable})
	if err != nil {
		t.Fatalf("the planner's sweepable amount must build: %v", err)
	}
	outputs, _ := outputsFrom(&txs[0])
	if len(inputsFrom(&txs[0])) != 2 || len(outputs) != 1 || outputs[0].Value != sweepable.Int64() {
		t.Fatalf("outputs %+v", outputs)
	}
	if _, err := live.BuildSweep(context.Background(), types.SweepRequest{From: feeTestFrom, To: feeTestTo, Amount: new(big.Int).Add(sweepable, big.NewInt(1))}); err == nil {
		t.Fatal("one sat more than sweepable must not build")
	}
}

func TestBuild_Sweep_RejectsDustAndMissingAddresses(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":1}`)
	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 600)}, true))
	live := feeTestAdapter(esplora)

	if _, err := live.BuildSweep(context.Background(), types.SweepRequest{From: feeTestFrom, To: feeTestTo}); err == nil {
		t.Fatal("600 sats minus the fee is dust; the sweep must fail")
	}
	if _, err := live.BuildSweep(context.Background(), types.SweepRequest{From: feeTestFrom}); err == nil {
		t.Fatal("missing destination accepted")
	}
	if _, err := live.BuildTransfer(context.Background(), types.TransferRequest{To: feeTestTo, Amount: big.NewInt(1_000)}); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestEstimate_Fee_MatchesTheBuiltTransaction(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":5}`)
	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 40_000), utxo(1, 40_000)}, true))
	live := feeTestAdapter(esplora)
	req := types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(60_000)}

	estimate, err := live.EstimateFee(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := live.BuildTransfer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmtUnits(big.NewInt(unsigned.Metadata["fee"].(int64)), 8); estimate.Fee != want || estimate.FeeAsset != "TBTC" {
		t.Fatalf("estimate %+v, built fee %s", estimate, want)
	}

	typical, err := live.EstimateFee(context.Background(), types.TransferRequest{To: feeTestTo})
	if err != nil || typical.Fee != fmtUnits(big.NewInt(rate(5).fee(1, 2)), 8) {
		t.Fatalf("typical %+v err %v", typical, err)
	}
}

func TestFee_Rate_IsCachedBriefly(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":5}`)
	live := feeTestAdapter(esplora)

	for i := 0; i < 3; i++ {
		if policy := live.feePolicy(context.Background()); policy.milliSatPerVByte != 5000 {
			t.Fatalf("policy %+v", policy)
		}
	}
	if hits := esplora.hitCount(esploraTestPrefix + "/fee-estimates"); hits != 1 {
		t.Fatalf("fee estimates fetched %d times", hits)
	}
}

func TestSpendable_Funds_CountsConfirmedUTXOsAndTheSweepFee(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":2}`)
	body := `[` +
		`{"txid":"` + utxo(0, 0).TxID + `","vout":0,"value":70000,"status":{"confirmed":true}},` +
		`{"txid":"` + utxo(1, 0).TxID + `","vout":1,"value":30000,"status":{"confirmed":true}},` +
		`{"txid":"` + utxo(2, 0).TxID + `","vout":2,"value":900000,"status":{"confirmed":false}}` +
		`]`
	esplora.ok(utxoPath(), body)

	funds, err := feeTestAdapter(esplora).SpendableFunds(context.Background(), feeTestFrom)

	if err != nil || funds.Balance.Int64() != 100_000 || funds.MaxTransferFee.Int64() != rate(2).fee(2, 1) {
		t.Fatalf("funds %+v err %v", funds, err)
	}
}

func TestSpendable_Funds_EdgeCases(t *testing.T) {
	t.Run("nothing confirmed", func(t *testing.T) {
		esplora := newFakeEsplora(t)
		esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 50_000)}, false))
		funds, err := feeTestAdapter(esplora).SpendableFunds(context.Background(), feeTestFrom)
		if err != nil || funds.Balance.Sign() != 0 || funds.MaxTransferFee.Sign() != 0 {
			t.Fatalf("funds %+v err %v", funds, err)
		}
		if esplora.hitCount(esploraTestPrefix+"/fee-estimates") != 0 {
			t.Fatal("an empty address needs no fee estimate")
		}
	})
	t.Run("leftover below dust cannot send anything", func(t *testing.T) {
		esplora := newFakeEsplora(t)
		esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":1}`)
		// 600 − 110 vB × 1 sat = 490, below dust.
		esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 600)}, true))
		funds, err := feeTestAdapter(esplora).SpendableFunds(context.Background(), feeTestFrom)
		if err != nil || funds.Balance.Int64() != 600 || funds.MaxTransferFee.Int64() != 600 {
			t.Fatalf("funds %+v err %v", funds, err)
		}
	})
	t.Run("utxo api failure", func(t *testing.T) {
		esplora := newFakeEsplora(t)
		esplora.on(utxoPath(), esploraAnswer{http.StatusBadGateway, "down"})
		if _, err := feeTestAdapter(esplora).SpendableFunds(context.Background(), feeTestFrom); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("blank address", func(t *testing.T) {
		if _, err := feeTestAdapter(newFakeEsplora(t)).SpendableFunds(context.Background(), " "); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestSmart_Fee_RateFromBitcoind(t *testing.T) {
	live := fakeBitcoind(t, map[string]string{
		"estimatesmartfee": `{"jsonrpc":"2.0","id":1,"result":{"feerate":0.00012345,"blocks":3}}`,
	})
	if got, err := live.fetchFeeRate(context.Background()); err != nil || got != 12345 {
		t.Fatalf("rate %d err %v, want 12.345 sat/vB", got, err)
	}

	noData := fakeBitcoind(t, map[string]string{
		"estimatesmartfee": `{"jsonrpc":"2.0","id":1,"result":{"errors":["Insufficient data or no feerate found"],"blocks":0}}`,
	})
	if policy := noData.feePolicy(context.Background()); policy.milliSatPerVByte != 0 || policy.flatFee != flatTestFee {
		t.Fatalf("policy %+v, want the flat fallback", policy)
	}
}
