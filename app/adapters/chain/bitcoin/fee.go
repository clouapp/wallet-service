package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// Confirmation targets read from Esplora /fee-estimates, first present wins.
	btcFeeTargetMinBlocks = 3
	btcFeeTargetMaxBlocks = 6
	// bitcoind estimatesmartfee target.
	btcSmartFeeTargetBlocks = btcFeeTargetMinBlocks

	// Rates are kept in milli-satoshis per vbyte so fees are exact integers.
	milliSatsPerSat = 1000
	// btcMinRelayMilliSatPerVByte is the default min relay fee (1 sat/vB): no fee is
	// ever below it, whatever the estimator says.
	btcMinRelayMilliSatPerVByte = 1 * milliSatsPerSat
	// btcMaxSaneSatPerVByte rejects an estimator answer as garbage above it.
	btcMaxSaneSatPerVByte = 10_000

	// P2WPKH virtual size in half-vbytes: 10.5 vB overhead, 68 vB per input,
	// 31 vB per output.
	btcTxOverheadHalfVBytes   = 21
	btcP2WPKHInputHalfVBytes  = 136
	btcP2WPKHOutputHalfVBytes = 62

	btcOutputsPaymentOnly       = 1
	btcOutputsPaymentWithChange = 2
	btcTypicalInputs            = 1

	btcFeeRateCacheTTL = 30 * time.Second
	satsPerBTC         = 100_000_000
)

// btcFeePolicy prices a P2WPKH transaction. milliSatPerVByte is the estimated rate;
// 0 means the estimator was unavailable and flatFee applies.
type btcFeePolicy struct {
	milliSatPerVByte int64
	flatFee          int64
}

// p2wpkhVSize is ceil(10.5 + 68×inputs + 31×outputs).
func p2wpkhVSize(inputs, outputs int) int64 {
	halves := int64(btcTxOverheadHalfVBytes) +
		int64(btcP2WPKHInputHalfVBytes)*int64(inputs) +
		int64(btcP2WPKHOutputHalfVBytes)*int64(outputs)
	return (halves + 1) / 2
}

// fee is ceil(rate × vsize), never below the min relay fee for vsize. With no
// estimated rate it is the flat fallback fee, raised to the min relay fee when the
// transaction is large enough to need more.
func (p btcFeePolicy) fee(inputs, outputs int) int64 {
	vsize := p2wpkhVSize(inputs, outputs)
	relayFloor := ceilDiv(vsize*btcMinRelayMilliSatPerVByte, milliSatsPerSat)
	if p.milliSatPerVByte <= 0 {
		return max(p.flatFee, relayFloor)
	}
	return max(ceilDiv(vsize*p.milliSatPerVByte, milliSatsPerSat), relayFloor)
}

func ceilDiv(numerator, denominator int64) int64 {
	return (numerator + denominator - 1) / denominator
}

// btcFeeRateCache keeps the last estimated rate briefly so a plan over many
// addresses and the build that follows price transactions alike.
type btcFeeRateCache struct {
	mu               sync.Mutex
	milliSatPerVByte int64
	fetchedAt        time.Time
}

func (c *btcFeeRateCache) get(now time.Time) (int64, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.milliSatPerVByte <= 0 || now.Sub(c.fetchedAt) >= btcFeeRateCacheTTL {
		return 0, false
	}
	return c.milliSatPerVByte, true
}

func (c *btcFeeRateCache) put(rate int64, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.milliSatPerVByte = rate
	c.fetchedAt = now
}

// flatBTCFee is the fallback fee: btcFeeVBytes at the configured (or default) rate,
// adjusted by the wallet's fee policy.
func (a *BitcoinLive) flatBTCFee() int64 {
	rate := btcDefaultFeeRate
	if a.cfg.FeeRateDefault > 0 {
		rate = a.cfg.FeeRateDefault
	}
	milliSatPerVByte := a.fee.AdjustMilliSatRate(int64(rate) * milliSatsPerSat)
	return ceilDiv(int64(btcFeeVBytes)*milliSatPerVByte, milliSatsPerSat)
}

// feePolicy prices transactions at the estimated rate for a 3-6 block target, or at
// the flat fallback fee when the estimator fails or answers garbage. Both are
// adjusted by the wallet's fee policy; the cache keeps the network's rate.
func (a *BitcoinLive) feePolicy(ctx context.Context) btcFeePolicy {
	policy := btcFeePolicy{flatFee: a.flatBTCFee()}
	now := time.Now()
	if rate, ok := a.feeRates.get(now); ok {
		policy.milliSatPerVByte = a.fee.AdjustMilliSatRate(rate)
		return policy
	}
	rate, err := a.fetchFeeRate(ctx)
	if err != nil {
		slog.Warn("btc fee estimate unavailable, using the flat fee", "chain", a.cfg.ChainIDStr, "flat_fee_sats", policy.flatFee, "error", err)
		return policy
	}
	a.feeRates.put(rate, now)
	policy.milliSatPerVByte = a.fee.AdjustMilliSatRate(rate)
	return policy
}

func (a *BitcoinLive) fetchFeeRate(ctx context.Context) (int64, error) {
	if a.restAPI {
		body, err := a.esploraGet(ctx, "/fee-estimates")
		if err != nil {
			return 0, err
		}
		return parseEsploraFeeEstimates(body)
	}
	return a.fetchSmartFeeRate(ctx)
}

// parseEsploraFeeEstimates reads {"<blocks>": sat/vB, ...} and returns the rate of
// the first target present in 3..6 blocks, floored at the min relay fee.
func parseEsploraFeeEstimates(body []byte) (int64, error) {
	var estimates map[string]float64
	if err := json.Unmarshal(body, &estimates); err != nil {
		return 0, fmt.Errorf("parse fee estimates: %w", err)
	}
	for target := btcFeeTargetMinBlocks; target <= btcFeeTargetMaxBlocks; target++ {
		satPerVByte, ok := estimates[strconv.Itoa(target)]
		if !ok {
			continue
		}
		return milliSatRate(satPerVByte, fmt.Sprintf("%d-block estimate", target))
	}
	return 0, fmt.Errorf("fee estimates have no %d-%d block target", btcFeeTargetMinBlocks, btcFeeTargetMaxBlocks)
}

// fetchSmartFeeRate asks bitcoind (estimatesmartfee, BTC/kvB).
func (a *BitcoinLive) fetchSmartFeeRate(ctx context.Context) (int64, error) {
	var estimate struct {
		FeeRate *float64 `json:"feerate"`
		Errors  []string `json:"errors"`
	}
	if err := a.rpc.Call(ctx, "estimatesmartfee", &estimate, btcSmartFeeTargetBlocks); err != nil {
		return 0, err
	}
	if estimate.FeeRate == nil {
		return 0, fmt.Errorf("estimatesmartfee returned no rate: %s", strings.Join(estimate.Errors, "; "))
	}
	// BTC/kvB × 1e8 sat/BTC ÷ 1000 vB/kvB = sat/vB.
	return milliSatRate(*estimate.FeeRate*(satsPerBTC/milliSatsPerSat), "estimatesmartfee")
}

func milliSatRate(satPerVByte float64, source string) (int64, error) {
	if math.IsNaN(satPerVByte) || math.IsInf(satPerVByte, 0) || satPerVByte <= 0 || satPerVByte > btcMaxSaneSatPerVByte {
		return 0, fmt.Errorf("%s of %v sat/vB is not a usable fee rate", source, satPerVByte)
	}
	rate := int64(math.Round(satPerVByte * milliSatsPerSat))
	return max(rate, btcMinRelayMilliSatPerVByte), nil
}

// ---------------------------------------------------------------------------
// Coin selection
// ---------------------------------------------------------------------------

// btcSpend is a chosen set of inputs paying one amount: change is 0 when there is
// no change output (anything below dust left over went to the fee).
type btcSpend struct {
	inputs []btcInput
	sum    int64
	fee    int64
	change int64
}

func (s btcSpend) outputs(from, to string, amount int64) []btcOutput {
	return btcPaymentOutputs(from, to, amount, s.change)
}

// spendFromInputs pays amount from exactly inputs: with change when the change after
// the two-output fee is at least dust, otherwise without change when the inputs cover
// the one-output fee, the leftover going to the fee.
func spendFromInputs(inputs []btcInput, amount int64, policy btcFeePolicy) (btcSpend, bool) {
	if len(inputs) == 0 {
		return btcSpend{}, false
	}
	sum := sumBTCInputs(inputs)
	owned := append([]btcInput(nil), inputs...)
	withChange := policy.fee(len(inputs), btcOutputsPaymentWithChange)
	if change := sum - amount - withChange; change >= btcDustSats {
		return btcSpend{inputs: owned, sum: sum, fee: withChange, change: change}, true
	}
	if leftover := sum - amount; leftover >= policy.fee(len(inputs), btcOutputsPaymentOnly) {
		return btcSpend{inputs: owned, sum: sum, fee: leftover}, true
	}
	return btcSpend{}, false
}

// selectBTCSpend adds UTXOs largest first until they pay amount and their own fee.
// Whenever amount ≤ maxSendableSats(utxos) it succeeds (at the latest with every UTXO
// and no change), which is what the sweep planner relies on.
func selectBTCSpend(utxos []btcInput, amount int64, policy btcFeePolicy) (btcSpend, error) {
	if amount < btcDustSats {
		return btcSpend{}, fmt.Errorf("btc amount %d sats is below the %d sat dust limit", amount, btcDustSats)
	}
	ordered := spendableBTCInputs(utxos)
	for count := 1; count <= len(ordered); count++ {
		if spend, ok := spendFromInputs(ordered[:count], amount, policy); ok {
			return spend, nil
		}
	}
	return btcSpend{}, fmt.Errorf("insufficient funds: %d confirmed sats in %d utxos cannot pay %d sats plus fee %d",
		sumBTCInputs(ordered), len(ordered), amount, policy.fee(max(len(ordered), btcTypicalInputs), btcOutputsPaymentOnly))
}

// maxSendableSats is the most one transfer spending every UTXO can pay a single
// recipient after its fee; 0 when that is below dust.
func maxSendableSats(utxos []btcInput, policy btcFeePolicy) int64 {
	ordered := spendableBTCInputs(utxos)
	if len(ordered) == 0 {
		return 0
	}
	sendable := sumBTCInputs(ordered) - policy.fee(len(ordered), btcOutputsPaymentOnly)
	if sendable < btcDustSats {
		return 0
	}
	return sendable
}

// spendableBTCInputs drops worthless entries and orders the rest largest first.
func spendableBTCInputs(utxos []btcInput) []btcInput {
	ordered := make([]btcInput, 0, len(utxos))
	for _, utxo := range utxos {
		if utxo.Value > 0 {
			ordered = append(ordered, utxo)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Value > ordered[j].Value })
	return ordered
}

func sumBTCInputs(inputs []btcInput) int64 {
	var sum int64
	for _, in := range inputs {
		sum += in.Value
	}
	return sum
}

// estimateTransferFeeSats is the fee BuildTransfer would pay for req, or a typical
// one-input transfer with change when From, Amount or its UTXOs cannot say.
func (a *BitcoinLive) estimateTransferFeeSats(ctx context.Context, req types.TransferRequest) int64 {
	if strings.TrimSpace(req.From) == "" || req.Amount == nil || !req.Amount.IsInt64() || req.Amount.Sign() <= 0 {
		return a.feePolicy(ctx).fee(btcTypicalInputs, btcOutputsPaymentWithChange)
	}
	quote, err := a.QuoteTransferFee(ctx, req.From, req.Amount, nil)
	if err != nil || !quote.Covered {
		return a.feePolicy(ctx).fee(btcTypicalInputs, btcOutputsPaymentWithChange)
	}
	return quote.Fee
}

// QuoteTransferFee is the fee BuildTransfer would pay sending amount from `from`:
// the same confirmed UTXOs, fee policy and largest-first selection. pendingInputs
// are outputs the transfer will also be able to spend (sweep legs into `from`).
// A failed UTXO listing is returned as an error, never replaced by a typical fee.
func (a *BitcoinLive) QuoteTransferFee(ctx context.Context, from string, amount *big.Int, pendingInputs []*big.Int) (chain.BitcoinFeeQuote, error) {
	if strings.TrimSpace(from) == "" {
		return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: from address is required")
	}
	if amount == nil || !amount.IsInt64() || amount.Sign() <= 0 {
		return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: amount must be a positive number of sats")
	}
	utxos, err := a.listConfirmedUTXOs(ctx, from)
	if err != nil {
		return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: utxos of %s: %w", from, err)
	}
	for index, pending := range pendingInputs {
		if pending == nil || !pending.IsInt64() || pending.Sign() <= 0 {
			return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: pending input %d is not a positive number of sats", index)
		}
		utxos = append(utxos, btcInput{Value: pending.Int64(), Address: from})
	}
	policy := a.feePolicy(ctx)
	spend, err := selectBTCSpend(utxos, amount.Int64(), policy)
	if err != nil {
		return policy.quote(btcTypicalInputs, btcOutputsPaymentWithChange, policy.fee(btcTypicalInputs, btcOutputsPaymentWithChange), false), nil
	}
	outputs := btcOutputsPaymentOnly
	if spend.change > 0 {
		outputs = btcOutputsPaymentWithChange
	}
	return policy.quote(len(spend.inputs), outputs, spend.fee, true), nil
}

// QuoteSweepFee is the fee BuildSweep pays emptying every confirmed UTXO of `from`
// into one output. Covered is false when nothing above dust can be swept.
func (a *BitcoinLive) QuoteSweepFee(ctx context.Context, from string) (chain.BitcoinFeeQuote, error) {
	if strings.TrimSpace(from) == "" {
		return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: from address is required")
	}
	utxos, err := a.listConfirmedUTXOs(ctx, from)
	if err != nil {
		return chain.BitcoinFeeQuote{}, fmt.Errorf("btc fee quote: utxos of %s: %w", from, err)
	}
	policy := a.feePolicy(ctx)
	inputs := spendableBTCInputs(utxos)
	if maxSendableSats(inputs, policy) == 0 {
		return policy.quote(max(len(inputs), btcTypicalInputs), btcOutputsPaymentOnly, policy.fee(max(len(inputs), btcTypicalInputs), btcOutputsPaymentOnly), false), nil
	}
	return policy.quote(len(inputs), btcOutputsPaymentOnly, policy.fee(len(inputs), btcOutputsPaymentOnly), true), nil
}

func (p btcFeePolicy) quote(inputs, outputs int, fee int64, covered bool) chain.BitcoinFeeQuote {
	return chain.BitcoinFeeQuote{
		Fee:              fee,
		Inputs:           inputs,
		Outputs:          outputs,
		VSize:            p2wpkhVSize(inputs, outputs),
		MilliSatPerVByte: p.milliSatPerVByte,
		FlatFallback:     p.milliSatPerVByte <= 0,
		Covered:          covered,
	}
}

// MinimumTransferAmount is the smallest amount BuildTransfer accepts (the dust limit).
func (a *BitcoinLive) MinimumTransferAmount() *big.Int {
	return big.NewInt(btcDustSats)
}

// SpendableFunds reads address's confirmed UTXOs and prices spending them with the
// same fee policy and selection BuildTransfer / BuildSweep use. Displayed balances
// (GetBalance) still include unconfirmed UTXOs.
func (a *BitcoinLive) SpendableFunds(ctx context.Context, address string) (chain.SpendableFunds, error) {
	if strings.TrimSpace(address) == "" {
		return chain.SpendableFunds{}, fmt.Errorf("btc spendable funds: address is required")
	}
	utxos, err := a.listConfirmedUTXOs(ctx, address)
	if err != nil {
		return chain.SpendableFunds{}, fmt.Errorf("btc spendable funds of %s: %w", address, err)
	}
	balance := sumBTCInputs(spendableBTCInputs(utxos))
	if balance == 0 {
		return chain.SpendableFunds{Balance: new(big.Int), MaxTransferFee: new(big.Int)}, nil
	}
	fee := balance - maxSendableSats(utxos, a.feePolicy(ctx))
	return chain.SpendableFunds{Balance: big.NewInt(balance), MaxTransferFee: big.NewInt(fee)}, nil
}
