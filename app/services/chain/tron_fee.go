package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// tronMaxResultSizeInTx is what java-tron adds per contract to a transaction's
	// serialized size when it charges bandwidth (Constant.MAX_RESULT_SIZE_IN_TX).
	tronMaxResultSizeInTx = 64
	// tronChainParamsTTL keeps the committee-set prices; they change by proposal only.
	tronChainParamsTTL = 10 * time.Minute
	// tronEnergyMarginPercent pads the simulated energy: a popular contract's
	// dynamic-energy factor can rise at the next maintenance period (every 6 h).
	tronEnergyMarginPercent = int64(120)
	// tronFeeLimitCapSun is the most energy fee one TRC-20 transfer may commit
	// (150 TRX, TronWeb's default fee_limit). A larger estimate fails instead of being
	// clamped, which would only make the call run out of energy and burn the fee.
	tronFeeLimitCapSun = int64(150_000_000)
	// tronTRC20ReferenceEnergy prices a TRC-20 transfer whose sender holds none of the
	// token and so cannot be simulated: the energy_used (dynamic penalty included) of
	// a mainnet USDT transfer to a never-funded address, 130 285 in October 2026.
	tronTRC20ReferenceEnergy = int64(130_285)
	// tronSunPerFeeUnit is the "gas price" the sweep planner multiplies fee units by:
	// TRON fees have no auction, so a transfer's gas limit is its whole fee in sun.
	tronSunPerFeeUnit = int64(1)

	tronTRC20TransferSelector = "transfer(address,uint256)"
	tronTRC20BalanceSelector  = "balanceOf(address)"
	tronABIWordBytes          = 32

	tronParamTransactionFee      = "getTransactionFee"
	tronParamEnergyFee           = "getEnergyFee"
	tronParamCreateAccountFee    = "getCreateAccountFee"
	tronParamCreateNewAccountFee = "getCreateNewAccountFeeInSystemContract"
	tronParamMaxFeeLimit         = "getMaxFeeLimit"
)

// tronFeeProbeRecipient (41cb0600…, the last 20 bytes of sha256("macro-wallets tron
// fee probe")) prices transfers whose recipient is not known yet. It is treated as
// never activated and holds no token, so a quote is the worst case: TRX pays the
// account activation, TRC-20 the first-time storage of the recipient's balance.
const tronFeeProbeRecipient = "TUUhMwaBT4s7Bd5AmAYUh4pJFQMViufXRL"

// tronFeeProbeAmount sizes a TRX transfer whose amount is unknown with the widest
// varint an amount can take.
const tronFeeProbeAmount = int64(math.MaxInt64)

var tronTRC20TransferMethodID = []byte{0xa9, 0x05, 0x9c, 0xbb}

// TronFeeProbeRecipient is the recipient a fee quote uses when the real one is not
// known yet.
func TronFeeProbeRecipient() string { return tronFeeProbeRecipient }

// tronChainParams are the network's resource prices, in sun.
type tronChainParams struct {
	sunPerBandwidthByte int64
	sunPerEnergy        int64
	createAccountFee    int64
	createNewAccountFee int64
	maxFeeLimit         int64
}

type tronChainParamsCache struct {
	mu        sync.Mutex
	params    tronChainParams
	fetchedAt time.Time
}

// chainParams reads /wallet/getchainparameters, cached for tronChainParamsTTL. A
// missing or non-positive price is an error: fees are never guessed.
func (a *TronLive) chainParams(ctx context.Context) (tronChainParams, error) {
	a.params.mu.Lock()
	defer a.params.mu.Unlock()
	if !a.params.fetchedAt.IsZero() && a.now().Sub(a.params.fetchedAt) < tronChainParamsTTL {
		return a.params.params, nil
	}
	var response struct {
		ChainParameter []struct {
			Key   string `json:"key"`
			Value int64  `json:"value"`
		} `json:"chainParameter"`
	}
	if err := a.post(ctx, "/wallet/getchainparameters", map[string]any{}, &response); err != nil {
		return tronChainParams{}, fmt.Errorf("tron chain parameters: %w", err)
	}
	values := make(map[string]int64, len(response.ChainParameter))
	for _, parameter := range response.ChainParameter {
		values[parameter.Key] = parameter.Value
	}
	required := func(key string) (int64, error) {
		value, ok := values[key]
		if !ok || value <= 0 {
			return 0, fmt.Errorf("tron chain parameter %s is missing or not positive (%d)", key, value)
		}
		return value, nil
	}
	var params tronChainParams
	var err error
	for _, field := range []struct {
		key    string
		target *int64
	}{
		{tronParamTransactionFee, &params.sunPerBandwidthByte},
		{tronParamEnergyFee, &params.sunPerEnergy},
		{tronParamCreateAccountFee, &params.createAccountFee},
		{tronParamCreateNewAccountFee, &params.createNewAccountFee},
		{tronParamMaxFeeLimit, &params.maxFeeLimit},
	} {
		if *field.target, err = required(field.key); err != nil {
			return tronChainParams{}, err
		}
	}
	a.params.params, a.params.fetchedAt = params, a.now()
	return params, nil
}

// TronFeeQuote prices one transfer as BuildTransfer encodes it, in sun. Every
// resource is paid by burning TRX: no free or staked bandwidth/energy is assumed.
//   - Bandwidth: (signed size + 64) bytes × getTransactionFee, except for a TRX
//     transfer that activates its recipient, which java-tron bills instead as
//     getCreateNewAccountFeeInSystemContract + getCreateAccountFee (ActivationFee).
//   - Energy (TRC-20): FeeLimit = simulated energy × getEnergyFee × 120 %, the
//     fee_limit encoded in the transaction and the most its call can burn.
type TronFeeQuote struct {
	BandwidthBytes      int64
	SunPerBandwidthByte int64
	BandwidthFee        *big.Int
	ActivationFee       *big.Int
	Energy              int64
	SunPerEnergy        int64
	FeeLimit            *big.Int
	// EnergyIsReference: the sender holds none of the token, so Energy is
	// tronTRC20ReferenceEnergy rather than a simulation.
	EnergyIsReference bool
}

// Fee is the most the transfer can cost its sender besides the amount.
func (q TronFeeQuote) Fee() *big.Int {
	fee := new(big.Int)
	for _, part := range []*big.Int{q.BandwidthFee, q.ActivationFee, q.FeeLimit} {
		if part != nil {
			fee.Add(fee, part)
		}
	}
	return fee
}

// tronTransferPlan is a transfer ready to encode, with its price.
type tronTransferPlan struct {
	contract      tronContract
	quote         TronFeeQuote
	from          string
	to            string
	tokenContract string
	amount        *big.Int
}

func (p tronTransferPlan) feeLimit() int64 {
	if p.quote.FeeLimit == nil {
		return 0
	}
	return p.quote.FeeLimit.Int64()
}

type tronEnergyMode int

const (
	// tronEnergyStrict builds: the sender must hold the amount it sends.
	tronEnergyStrict tronEnergyMode = iota
	// tronEnergyQuote estimates: a sender holding less is simulated with what it
	// holds (energy does not depend on the amount), one holding none gets the
	// reference energy.
	tronEnergyQuote
)

// QuoteTransferFee prices req the way BuildTransfer would encode it. An empty
// recipient is the probe; an unknown TRX amount is sized with the widest varint.
func (a *TronLive) QuoteTransferFee(ctx context.Context, req types.TransferRequest) (TronFeeQuote, error) {
	params, err := a.chainParams(ctx)
	if err != nil {
		return TronFeeQuote{}, err
	}
	var plan tronTransferPlan
	if req.Token == nil {
		plan, err = a.planNativeTransfer(ctx, params, req.From, req.To, req.Amount, false)
	} else {
		plan, err = a.planTokenTransfer(ctx, params, req, tronEnergyQuote)
	}
	if err != nil {
		return TronFeeQuote{}, err
	}
	return plan.quote, nil
}

// EstimateTransferGasLimit returns req's whole fee in sun (EstimateGasPrice is 1),
// so the sweep planner and the fee quote sum TRON fees with their gas arithmetic.
// A caller-supplied req.GasLimit wins.
func (a *TronLive) EstimateTransferGasLimit(ctx context.Context, req types.TransferRequest) (uint64, error) {
	if req.GasLimit != nil && *req.GasLimit > 0 {
		return *req.GasLimit, nil
	}
	quote, err := a.QuoteTransferFee(ctx, req)
	if err != nil {
		return 0, err
	}
	return tronFeeUnits(quote.Fee())
}

// NativeTransferGasLimit is the whole fee in sun of a TRX transfer from → to (a
// gas_seed); empty addresses are the probe.
func (a *TronLive) NativeTransferGasLimit(ctx context.Context, from, to string) (uint64, error) {
	return a.EstimateTransferGasLimit(ctx, types.TransferRequest{From: from, To: to, Asset: a.cfg.NativeSymbol})
}

func tronFeeUnits(fee *big.Int) (uint64, error) {
	if fee == nil || fee.Sign() <= 0 || !fee.IsUint64() {
		return 0, fmt.Errorf("%w: tron fee %v is not a positive sun amount", ErrGasEstimateFailed, fee)
	}
	return fee.Uint64(), nil
}

// EstimateGasPrice is 1 sun per fee unit: see EstimateTransferGasLimit.
func (a *TronLive) EstimateGasPrice(ctx context.Context) (*big.Int, error) {
	return big.NewInt(tronSunPerFeeUnit), nil
}

func (a *TronLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	quote, err := a.QuoteTransferFee(ctx, req)
	if err != nil {
		return nil, err
	}
	return &types.FeeEstimate{Fee: fmtUnits(quote.Fee(), tronNativeDecimals), FeeAsset: a.cfg.NativeSymbol}, nil
}

// NativeTransferReserve is the most a TRX transfer to any recipient costs: the
// larger of the activation fee and the bandwidth of a widest-amount transfer.
// TRON accounts keep no minimum balance.
func (a *TronLive) NativeTransferReserve(ctx context.Context) (fee, minimumRemaining *big.Int, err error) {
	params, err := a.chainParams(ctx)
	if err != nil {
		return nil, nil, err
	}
	plan, err := a.planNativeTransfer(ctx, params, "", "", nil, false)
	if err != nil {
		return nil, nil, err
	}
	bandwidth := new(big.Int).Mul(big.NewInt(plan.quote.BandwidthBytes), big.NewInt(params.sunPerBandwidthByte))
	reserve := plan.quote.Fee()
	if bandwidth.Cmp(reserve) > 0 {
		reserve = bandwidth
	}
	return reserve, new(big.Int), nil
}

// planNativeTransfer prices a TRX transfer. strict (building) requires both
// addresses and a positive int64 amount; otherwise empty addresses are the probe
// and a nil amount is sized with tronFeeProbeAmount.
func (a *TronLive) planNativeTransfer(ctx context.Context, params tronChainParams, from, to string, amount *big.Int, strict bool) (tronTransferPlan, error) {
	if strict && (from == "" || to == "") {
		return tronTransferPlan{}, fmt.Errorf("tron transfer: sender and recipient are required")
	}
	sizedAmount := tronFeeProbeAmount
	if amount != nil || strict {
		if amount == nil || amount.Sign() <= 0 || !amount.IsInt64() {
			return tronTransferPlan{}, fmt.Errorf("tron transfer: amount %v must be a positive int64 sun amount", amount)
		}
		sizedAmount = amount.Int64()
	}
	owner, err := tronRawOrProbe("sender", from)
	if err != nil {
		return tronTransferPlan{}, err
	}
	recipient, err := tronRawOrProbe("recipient", to)
	if err != nil {
		return tronTransferPlan{}, err
	}
	contract := tronContract{contractType: tronContractTypeTransfer, owner: owner, to: recipient, amount: sizedAmount}
	bandwidthBytes, err := tronSizingBandwidth(contract, 0, a.now())
	if err != nil {
		return tronTransferPlan{}, err
	}
	activated, err := a.recipientActivated(ctx, to)
	if err != nil {
		return tronTransferPlan{}, fmt.Errorf("tron recipient %s: %w", to, err)
	}
	quote := TronFeeQuote{
		BandwidthBytes:      bandwidthBytes,
		SunPerBandwidthByte: params.sunPerBandwidthByte,
		BandwidthFee:        new(big.Int),
		ActivationFee:       new(big.Int),
		SunPerEnergy:        params.sunPerEnergy,
		FeeLimit:            new(big.Int),
	}
	if activated {
		quote.BandwidthFee.Mul(big.NewInt(bandwidthBytes), big.NewInt(params.sunPerBandwidthByte))
	} else {
		quote.ActivationFee.SetInt64(params.createNewAccountFee)
		quote.ActivationFee.Add(quote.ActivationFee, big.NewInt(params.createAccountFee))
	}
	return tronTransferPlan{contract: contract, quote: quote, from: from, to: probeIfEmptyTron(to), amount: big.NewInt(sizedAmount)}, nil
}

// recipientActivated reports whether the account exists; the probe never does.
func (a *TronLive) recipientActivated(ctx context.Context, address string) (bool, error) {
	if address == "" || address == tronFeeProbeRecipient {
		return false, nil
	}
	_, found, err := a.account(ctx, address)
	return found, err
}

// planTokenTransfer prices a TRC-20 transfer(to, amount) from req.From: bandwidth
// plus the fee_limit covering the simulated energy.
func (a *TronLive) planTokenTransfer(ctx context.Context, params tronChainParams, req types.TransferRequest, mode tronEnergyMode) (tronTransferPlan, error) {
	if mode == tronEnergyQuote {
		req.To = probeIfEmptyTron(req.To)
	}
	if err := validateTokenTransferForEstimate(req); err != nil {
		return tronTransferPlan{}, err
	}
	owner, err := tronRaw("sender", req.From)
	if err != nil {
		return tronTransferPlan{}, err
	}
	recipient, err := tronRawOrProbe("recipient", req.To)
	if err != nil {
		return tronTransferPlan{}, err
	}
	contractAddress, err := tronRaw("token contract", req.Token.Contract)
	if err != nil {
		return tronTransferPlan{}, err
	}
	data, err := encodeTRC20Transfer(recipient, req.Amount)
	if err != nil {
		return tronTransferPlan{}, err
	}
	energy, reference, err := a.tokenTransferEnergy(ctx, req, recipient, mode)
	if err != nil {
		return tronTransferPlan{}, err
	}
	feeLimit, err := tronEnergyFeeLimit(energy, params)
	if err != nil {
		return tronTransferPlan{}, err
	}
	contract := tronContract{contractType: tronContractTypeTriggerSmart, owner: owner, contract: contractAddress, data: data}
	bandwidthBytes, err := tronSizingBandwidth(contract, feeLimit, a.now())
	if err != nil {
		return tronTransferPlan{}, err
	}
	quote := TronFeeQuote{
		BandwidthBytes:      bandwidthBytes,
		SunPerBandwidthByte: params.sunPerBandwidthByte,
		BandwidthFee:        new(big.Int).Mul(big.NewInt(bandwidthBytes), big.NewInt(params.sunPerBandwidthByte)),
		ActivationFee:       new(big.Int),
		Energy:              energy,
		SunPerEnergy:        params.sunPerEnergy,
		FeeLimit:            big.NewInt(feeLimit),
		EnergyIsReference:   reference,
	}
	return tronTransferPlan{
		contract: contract, quote: quote, from: req.From, to: probeIfEmptyTron(req.To),
		tokenContract: req.Token.Contract, amount: new(big.Int).Set(req.Amount),
	}, nil
}

// tronEnergyFeeLimit is ceil(energy × getEnergyFee × margin), refused above
// tronFeeLimitCapSun or the network's getMaxFeeLimit.
func tronEnergyFeeLimit(energy int64, params tronChainParams) (int64, error) {
	if energy <= 0 {
		return 0, fmt.Errorf("%w: tron energy estimate %d is not positive", ErrGasEstimateFailed, energy)
	}
	feeLimit := new(big.Int).Mul(big.NewInt(energy), big.NewInt(params.sunPerEnergy))
	feeLimit.Mul(feeLimit, big.NewInt(tronEnergyMarginPercent))
	feeLimit.Add(feeLimit, big.NewInt(percentDenominator-1))
	feeLimit.Div(feeLimit, big.NewInt(percentDenominator))
	ceiling := min(tronFeeLimitCapSun, params.maxFeeLimit)
	if feeLimit.Cmp(big.NewInt(ceiling)) > 0 {
		return 0, fmt.Errorf("%w: tron fee_limit %s sun for %d energy exceeds the %d sun ceiling", ErrGasEstimateFailed, feeLimit, energy, ceiling)
	}
	return feeLimit.Int64(), nil
}

// tronSizingBandwidth is the bandwidth java-tron charges for contract once signed,
// with the reference and timestamps BuildTransfer writes (same varint widths).
func tronSizingBandwidth(contract tronContract, feeLimit int64, now time.Time) (int64, error) {
	raw := tronRawData{
		refBlockBytes: make([]byte, tronRefBlockBytesSize),
		refBlockHash:  make([]byte, tronRefBlockHashSize),
		expiration:    now.Add(tronTxExpirationWindow).UnixMilli(),
		contract:      contract,
		timestamp:     now.UnixMilli(),
		feeLimit:      feeLimit,
	}
	rawBytes, err := raw.encode()
	if err != nil {
		return 0, err
	}
	return tronBandwidthBytes(rawBytes), nil
}

// tronBandwidthBytes is the bandwidth java-tron charges a one-contract, one-signature
// transaction with this raw data (BandwidthProcessor: serialized size without ret,
// plus MAX_RESULT_SIZE_IN_TX).
func tronBandwidthBytes(rawBytes []byte) int64 {
	return tronSignedSize(rawBytes) + tronMaxResultSizeInTx
}

// tokenTransferEnergy simulates req; see tronEnergyMode for senders that hold less
// than the amount.
func (a *TronLive) tokenTransferEnergy(ctx context.Context, req types.TransferRequest, recipient []byte, mode tronEnergyMode) (int64, bool, error) {
	balance, err := a.tokenBalance(ctx, req.From, req.Token.Contract)
	if err != nil {
		return 0, false, fmt.Errorf("%w: %s balance of %s: %v", ErrGasEstimateFailed, req.Token.Symbol, req.From, err)
	}
	simulated := req.Amount
	switch {
	case balance.Cmp(req.Amount) >= 0:
	case mode == tronEnergyStrict:
		return 0, false, fmt.Errorf("%w: %s holds %s %s, the transfer needs %s",
			ErrGasEstimateFailed, req.From, balance, req.Token.Symbol, req.Amount)
	case balance.Sign() > 0:
		simulated = balance
	default:
		return tronTRC20ReferenceEnergy, true, nil
	}
	data, err := encodeTRC20Transfer(recipient, simulated)
	if err != nil {
		return 0, false, err
	}
	energy, err := a.simulateEnergy(ctx, req.From, req.Token.Contract, data)
	if err != nil {
		return 0, false, fmt.Errorf("%w: %s transfer of %s from %s to %s: %v",
			ErrGasEstimateFailed, req.Token.Symbol, simulated, req.From, probeIfEmptyTron(req.To), err)
	}
	return energy, false, nil
}

// simulateEnergy asks /wallet/estimateenergy for the energy the call needs and
// falls back to triggerconstantcontract's energy_used (which already includes the
// dynamic-energy penalty) on nodes without it, such as TronGrid mainnet.
func (a *TronLive) simulateEnergy(ctx context.Context, owner, contract string, data []byte) (int64, error) {
	if !bytes.HasPrefix(data, tronTRC20TransferMethodID) {
		return 0, fmt.Errorf("tron energy simulation: calldata is not a transfer(address,uint256) call")
	}
	// The node derives the method id from function_selector; parameter is the
	// ABI-encoded arguments alone.
	request, err := tronContractCallRequest(owner, contract, tronTRC20TransferSelector, data[len(tronTRC20TransferMethodID):])
	if err != nil {
		return 0, err
	}
	var estimate struct {
		Result         tronCallResult `json:"result"`
		EnergyRequired int64          `json:"energy_required"`
	}
	estimateErr := a.post(ctx, "/wallet/estimateenergy", request, &estimate)
	if estimateErr == nil && estimate.Result.Result && estimate.EnergyRequired > 0 {
		return estimate.EnergyRequired, nil
	}
	if estimateErr == nil {
		estimateErr = fmt.Errorf("estimateenergy: %s", estimate.Result.describe())
	}
	constant, err := a.triggerConstant(ctx, request)
	if err != nil {
		return 0, fmt.Errorf("%v; triggerconstantcontract: %w", estimateErr, err)
	}
	if constant.EnergyUsed <= 0 {
		return 0, fmt.Errorf("%v; triggerconstantcontract reported no energy", estimateErr)
	}
	return constant.EnergyUsed, nil
}

type tronConstantResult struct {
	Result         tronCallResult `json:"result"`
	EnergyUsed     int64          `json:"energy_used"`
	ConstantResult []string       `json:"constant_result"`
}

// triggerConstant runs a read-only call; a revert (reported as result=true with a
// message) is an error.
func (a *TronLive) triggerConstant(ctx context.Context, request map[string]any) (tronConstantResult, error) {
	var response tronConstantResult
	if err := a.post(ctx, "/wallet/triggerconstantcontract", request, &response); err != nil {
		return tronConstantResult{}, err
	}
	if !response.Result.Result || response.Result.Message != "" {
		return tronConstantResult{}, fmt.Errorf("call failed: %s", response.Result.describe())
	}
	return response, nil
}

// tokenBalance is balanceOf(address) on contract.
func (a *TronLive) tokenBalance(ctx context.Context, address, contract string) (*big.Int, error) {
	holder, err := tronRaw("holder", address)
	if err != nil {
		return nil, err
	}
	request, err := tronContractCallRequest(address, contract, tronTRC20BalanceSelector, tronABIAddressWord(holder))
	if err != nil {
		return nil, err
	}
	response, err := a.triggerConstant(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("tron balanceOf(%s) on %s: %w", address, contract, err)
	}
	if len(response.ConstantResult) != 1 {
		return nil, fmt.Errorf("tron balanceOf(%s) on %s returned %d results", address, contract, len(response.ConstantResult))
	}
	word, err := hex.DecodeString(response.ConstantResult[0])
	if err != nil || len(word) != tronABIWordBytes {
		return nil, fmt.Errorf("tron balanceOf(%s) on %s returned %q, want one ABI word", address, contract, response.ConstantResult[0])
	}
	return new(big.Int).SetBytes(word), nil
}

// tronContractCallRequest is the body of triggerconstantcontract / estimateenergy:
// hex addresses (visible=false), the selector and the ABI-encoded parameters.
func tronContractCallRequest(owner, contract, selector string, parameters []byte) (map[string]any, error) {
	ownerHex, err := addressing.TronAddressToHex(owner)
	if err != nil {
		return nil, fmt.Errorf("tron call owner: %w", err)
	}
	contractHex, err := addressing.TronAddressToHex(contract)
	if err != nil {
		return nil, fmt.Errorf("tron call contract: %w", err)
	}
	return map[string]any{
		"owner_address":     ownerHex,
		"contract_address":  contractHex,
		"function_selector": selector,
		"parameter":         hex.EncodeToString(parameters),
	}, nil
}

// encodeTRC20Transfer is transfer(address,uint256) calldata: the method id, the
// recipient's 20-byte body left-padded to a word, then the amount.
func encodeTRC20Transfer(recipient []byte, amount *big.Int) ([]byte, error) {
	if err := requireTronRaw("recipient", recipient); err != nil {
		return nil, err
	}
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 8*tronABIWordBytes {
		return nil, fmt.Errorf("tron trc20 transfer: amount %v must be a positive uint256", amount)
	}
	data := make([]byte, 0, len(tronTRC20TransferMethodID)+2*tronABIWordBytes)
	data = append(data, tronTRC20TransferMethodID...)
	data = append(data, tronABIAddressWord(recipient)...)
	amountWord := make([]byte, tronABIWordBytes)
	amount.FillBytes(amountWord)
	return append(data, amountWord...), nil
}

// tronABIAddressWord is an ABI address word: the 20-byte body without the 0x41 prefix.
func tronABIAddressWord(raw []byte) []byte {
	word := make([]byte, tronABIWordBytes)
	copy(word[tronABIWordBytes-addressing.TronAddressBodySize:], raw[1:])
	return word
}

func tronRaw(field, address string) ([]byte, error) {
	raw, err := addressing.DecodeTronAddress(address)
	if err != nil {
		return nil, fmt.Errorf("tron %s: %w", field, err)
	}
	return raw, nil
}

func tronRawOrProbe(field, address string) ([]byte, error) {
	return tronRaw(field, probeIfEmptyTron(address))
}

func probeIfEmptyTron(address string) string {
	if strings.TrimSpace(address) == "" {
		return tronFeeProbeRecipient
	}
	return address
}
