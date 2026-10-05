package chain

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// tronNativeDecimals: 1 TRX = 1 000 000 sun.
	tronNativeDecimals = 6
	// tronTxExpirationWindow is how long after its reference block a built
	// transaction stays valid; java-tron accepts up to 24 h.
	tronTxExpirationWindow = 10 * time.Minute
	// tronBlockInterval is TRON's block time; inclusion is polled at this pace.
	tronBlockInterval = 3 * time.Second
	// tronInclusionTimeout bounds the wait for a gas seed to land in a block.
	tronInclusionTimeout = 90 * time.Second
	tronTxIDHexLength    = 2 * tronTxIDSize
	tronReceiptSuccess   = "SUCCESS"
	tronTxInfoFailed     = "FAILED"
)

// TronConfig is everything that differs between TRON networks (mainnet, Nile).
type TronConfig struct {
	ChainIDStr   string
	ChainName    string
	NativeSymbol string
	RPCURL       string
	// APIKey is the optional TronGrid key, sent as TRON-PRO-API-KEY and never logged.
	APIKey        string
	IsTestnet     bool
	Confirmations uint64
	// Tokens lists the registered TRC-20s for deposit event matching.
	Tokens []types.Token
	// GasReadinessThreshold is the minimum TRX (sun) BaseAddress must hold to be
	// gas-ready. nil when unset.
	GasReadinessThreshold *big.Int
	// DustThresholdNative is the minimum TRX (sun) on a child for sweep
	// eligibility. nil when unset.
	DustThresholdNative *big.Int
}

// TronLive is the TRON adapter: transactions are built locally in protobuf, signed
// over their txID (sha256 of raw_data), and fees are burned TRX for bandwidth,
// account activation and energy.
type TronLive struct {
	cfg          TronConfig
	http         *http.Client
	retry        rateLimitRetry
	params       *tronChainParamsCache
	tokens       map[[addressing.TronAddressBodySize]byte]types.Token
	tokensErr    error
	now          func() time.Time
	pollInterval time.Duration
}

var (
	_ types.Chain                 = (*TronLive)(nil)
	_ types.TransferGasEstimator  = (*TronLive)(nil)
	_ types.MPCSignatureFinalizer = (*TronLive)(nil)
)

func NewTronLive(cfg TronConfig) *TronLive {
	tokens, tokensErr := indexTronTokens(cfg.ChainIDStr, cfg.Tokens)
	return &TronLive{
		cfg:          cfg,
		http:         httpclient.New(tronHTTPTimeout),
		retry:        defaultRateLimitRetry(),
		params:       &tronChainParamsCache{},
		tokens:       tokens,
		tokensErr:    tokensErr,
		now:          time.Now,
		pollInterval: tronBlockInterval,
	}
}

// indexTronTokens keys this chain's registered tokens by the 20-byte contract body
// that event logs carry; an unparsable contract is kept as an error so the scanner
// fails instead of silently missing its deposits.
func indexTronTokens(chainID string, tokens []types.Token) (map[[addressing.TronAddressBodySize]byte]types.Token, error) {
	index := make(map[[addressing.TronAddressBodySize]byte]types.Token, len(tokens))
	for _, token := range tokens {
		if token.ChainID != chainID {
			continue
		}
		raw, err := addressing.DecodeTronAddress(token.Contract)
		if err != nil {
			return nil, fmt.Errorf("tron token %s contract: %w", token.Symbol, err)
		}
		var body [addressing.TronAddressBodySize]byte
		copy(body[:], raw[1:])
		index[body] = token
	}
	return index, nil
}

func (a *TronLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *TronLive) Name() string                  { return a.cfg.ChainName }
func (a *TronLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *TronLive) NativeAsset() string           { return a.cfg.NativeSymbol }

// IsTestnet reports whether the chain record points at a TRON test network (Nile).
func (a *TronLive) IsTestnet() bool { return a.cfg.IsTestnet }

func (a *TronLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	return "", fmt.Errorf("TRON key derivation not implemented here — addresses come from addressing.DeriveTronAddress")
}

func (a *TronLive) ValidateAddress(address string) bool {
	return addressing.IsTronAddress(address)
}

func (a *TronLive) GasReadinessThreshold() *big.Int { return a.cfg.GasReadinessThreshold }

// DustThreshold returns cfg.DustThresholdNative for TRX; tokens are converted from
// USD by the sweep planner.
func (a *TronLive) DustThreshold(asset string) *big.Int {
	if types.SameAssetSymbol(asset, a.cfg.NativeSymbol) {
		return a.cfg.DustThresholdNative
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

type tronAccount struct {
	Address string `json:"address"`
	Balance int64  `json:"balance"`
}

// account reads /wallet/getaccount; a never-activated address answers {} and
// returns found=false.
func (a *TronLive) account(ctx context.Context, address string) (tronAccount, bool, error) {
	hexAddress, err := addressing.TronAddressToHex(address)
	if err != nil {
		return tronAccount{}, false, err
	}
	var account tronAccount
	if err := a.post(ctx, "/wallet/getaccount", map[string]any{"address": hexAddress}, &account); err != nil {
		return tronAccount{}, false, err
	}
	if account.Address == "" {
		return tronAccount{}, false, nil
	}
	if !strings.EqualFold(account.Address, hexAddress) {
		return tronAccount{}, false, fmt.Errorf("tron getaccount %s answered for %s", hexAddress, account.Address)
	}
	if account.Balance < 0 {
		return tronAccount{}, false, fmt.Errorf("tron getaccount %s: negative balance %d", address, account.Balance)
	}
	return account, true, nil
}

// GetBalance returns the TRX balance in sun; an address never activated holds 0.
func (a *TronLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	account, _, err := a.account(ctx, address)
	if err != nil {
		return nil, err
	}
	balance := big.NewInt(account.Balance)
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: balance, Decimals: tronNativeDecimals, Human: fmtUnits(balance, tronNativeDecimals)}, nil
}

// GetTokenBalance calls balanceOf(address) on the TRC-20 contract.
func (a *TronLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	balance, err := a.tokenBalance(ctx, address, token.Contract)
	if err != nil {
		return nil, err
	}
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: balance, Decimals: token.Decimals, Human: fmtUnits(balance, token.Decimals)}, nil
}

type tronHead struct {
	number    int64
	blockID   []byte
	timestamp int64
}

type tronBlockHeaderJSON struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number    int64 `json:"number"`
			Timestamp int64 `json:"timestamp"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

// headBlock reads the latest block header (/wallet/getblock without transactions,
// falling back to /wallet/getnowblock on nodes that predate it).
func (a *TronLive) headBlock(ctx context.Context) (tronHead, error) {
	var block tronBlockHeaderJSON
	err := a.post(ctx, "/wallet/getblock", map[string]any{"detail": false}, &block)
	var statusErr *tronStatusError
	if errors.As(err, &statusErr) {
		block = tronBlockHeaderJSON{}
		err = a.post(ctx, "/wallet/getnowblock", map[string]any{}, &block)
	}
	if err != nil {
		return tronHead{}, err
	}
	return parseTronBlockHeader(block, 0)
}

// parseTronBlockHeader validates a block header; wantNumber 0 accepts any number.
// A TRON block id starts with the block number, which is checked too.
func parseTronBlockHeader(block tronBlockHeaderJSON, wantNumber uint64) (tronHead, error) {
	number := block.BlockHeader.RawData.Number
	if number <= 0 {
		return tronHead{}, fmt.Errorf("tron block has no number")
	}
	if wantNumber != 0 && uint64(number) != wantNumber {
		return tronHead{}, fmt.Errorf("tron block number %d, want %d", number, wantNumber)
	}
	blockID, err := hex.DecodeString(block.BlockID)
	if err != nil || len(blockID) != tronBlockIDSize {
		return tronHead{}, fmt.Errorf("tron block %d has an invalid id %q", number, block.BlockID)
	}
	if binary.BigEndian.Uint64(blockID[:8]) != uint64(number) {
		return tronHead{}, fmt.Errorf("tron block id %s does not encode number %d", block.BlockID, number)
	}
	if block.BlockHeader.RawData.Timestamp <= 0 {
		return tronHead{}, fmt.Errorf("tron block %d has no timestamp", number)
	}
	return tronHead{number: number, blockID: blockID, timestamp: block.BlockHeader.RawData.Timestamp}, nil
}

func (a *TronLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	head, err := a.headBlock(ctx)
	if err != nil {
		return 0, err
	}
	return uint64(head.number), nil
}

type tronTransactionInfo struct {
	ID          string `json:"id"`
	BlockNumber int64  `json:"blockNumber"`
	Result      string `json:"result"`
	ResMessage  string `json:"resMessage"`
	Receipt     struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Log []tronEventLog `json:"log"`
}

// failure describes an on-chain failure ("" when the transaction succeeded). TRX
// transfers carry no receipt result; contract calls carry SUCCESS or the VM error.
func (info tronTransactionInfo) failure() string {
	if info.Result == tronTxInfoFailed {
		return strings.TrimSpace(info.Receipt.Result + " " + decodeTronMessage(info.ResMessage))
	}
	if info.Receipt.Result != "" && info.Receipt.Result != tronReceiptSuccess {
		return info.Receipt.Result
	}
	return ""
}

// GetTransactionBlock returns the block that included txHash, or 0 while the node
// does not know it. A transaction included but failed on-chain (a reverted or
// out-of-energy TRC-20 call, whose fee is still burned) returns an error, so the
// confirmation loop never counts it as a completed transfer.
func (a *TronLive) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	txID, err := normalizeTronTxID(txHash)
	if err != nil {
		return 0, err
	}
	var info tronTransactionInfo
	if err := a.post(ctx, "/wallet/gettransactioninfobyid", map[string]any{"value": txID}, &info); err != nil {
		return 0, err
	}
	if info.ID == "" || info.BlockNumber <= 0 {
		return 0, nil
	}
	if !strings.EqualFold(info.ID, txID) {
		return 0, fmt.Errorf("tron gettransactioninfobyid %s answered for %s", txID, info.ID)
	}
	if failure := info.failure(); failure != "" {
		return 0, fmt.Errorf("tron transaction %s failed on-chain in block %d: %s", txID, info.BlockNumber, failure)
	}
	return uint64(info.BlockNumber), nil
}

// AwaitTransactionIncluded polls until txHash is in a block. A gas seed must land
// before its child spends: the node refuses transactions from an account that does
// not exist yet or cannot pay its bandwidth.
func (a *TronLive) AwaitTransactionIncluded(ctx context.Context, txHash string) error {
	waitCtx, cancel := context.WithTimeout(ctx, tronInclusionTimeout)
	defer cancel()
	for {
		block, err := a.GetTransactionBlock(waitCtx, txHash)
		if err != nil {
			return fmt.Errorf("await tron transaction %s: %w", txHash, err)
		}
		if block > 0 {
			return nil
		}
		if err := sleepContext(waitCtx, a.pollInterval); err != nil {
			return fmt.Errorf("await tron transaction %s: not in a block after %s: %w", txHash, tronInclusionTimeout, err)
		}
	}
}

func normalizeTronTxID(txHash string) (string, error) {
	txID := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(txHash), "0x"))
	if len(txID) != tronTxIDHexLength {
		return "", fmt.Errorf("tron txid %q is not %d hex characters", txHash, tronTxIDHexLength)
	}
	if _, err := hex.DecodeString(txID); err != nil {
		return "", fmt.Errorf("tron txid %q is not hex", txHash)
	}
	return txID, nil
}

// ---------------------------------------------------------------------------
// Building
// ---------------------------------------------------------------------------

// BuildTransfer builds a TRX TransferContract or a TRC-20 transfer(to, amount)
// TriggerSmartContract referencing the latest block. A TRC-20 sender must hold the
// amount: the call would revert and still burn its energy.
func (a *TronLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	params, err := a.chainParams(ctx)
	if err != nil {
		return nil, err
	}
	var plan tronTransferPlan
	if req.Token == nil {
		plan, err = a.planNativeTransfer(ctx, params, req.From, req.To, req.Amount, true)
	} else {
		plan, err = a.planTokenTransfer(ctx, params, req, tronEnergyStrict)
	}
	if err != nil {
		return nil, err
	}
	return a.buildFromPlan(ctx, plan)
}

func (a *TronLive) buildFromPlan(ctx context.Context, plan tronTransferPlan) (*types.UnsignedTx, error) {
	head, err := a.headBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("tron reference block: %w", err)
	}
	ref, err := newTronRefBlock(head.number, head.blockID)
	if err != nil {
		return nil, err
	}
	raw := tronRawData{
		refBlockBytes: ref.bytes,
		refBlockHash:  ref.hash,
		expiration:    head.timestamp + tronTxExpirationWindow.Milliseconds(),
		contract:      plan.contract,
		timestamp:     a.now().UnixMilli(),
		feeLimit:      plan.feeLimit(),
	}
	rawBytes, err := raw.encode()
	if err != nil {
		return nil, err
	}
	if size := tronBandwidthBytes(rawBytes); size > plan.quote.BandwidthBytes {
		return nil, fmt.Errorf("tron transaction is %d bandwidth bytes, priced for %d", size, plan.quote.BandwidthBytes)
	}
	txID := tronTxID(rawBytes)
	unsigned := &types.UnsignedTx{
		ChainID:        a.cfg.ChainIDStr,
		RawBytes:       txID,
		TransferAmount: new(big.Int).Set(plan.amount),
		Metadata: map[string]interface{}{
			"raw_data_hex":  hex.EncodeToString(rawBytes),
			"tx_id":         hex.EncodeToString(txID),
			"contract_type": plan.contract.name(),
			"owner_address": plan.from,
			"to":            plan.to,
			"amount":        plan.amount.String(),
			"fee":           plan.quote.Fee().String(),
			"fee_limit":     raw.feeLimit,
			"expiration":    raw.expiration,
		},
	}
	if plan.tokenContract != "" {
		unsigned.Metadata["token_contract"] = plan.tokenContract
	}
	return unsigned, nil
}

// BuildSweep moves req.Asset from the child req.From to the base req.To.
// TRX: one transfer of the balance minus its fee. TRC-20: the token transfer,
// preceded by a gas_seed (base → child TRX, which also activates the child) of
// what the child lacks to pay the transfer's estimated bandwidth and energy plus
// tronGasSeedMarginPercent (see requiredSweepFunding).
func (a *TronLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	params, err := a.chainParams(ctx)
	if err != nil {
		return nil, err
	}
	if req.Token == nil {
		return a.buildNativeSweep(ctx, params, req)
	}

	amount := req.Amount
	if amount == nil {
		balance, err := a.tokenBalance(ctx, req.From, req.Token.Contract)
		if err != nil {
			return nil, fmt.Errorf("get token balance: %w", err)
		}
		amount = balance
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("no token balance to sweep")
	}
	sweepPlan, err := a.planTokenTransfer(ctx, params, types.TransferRequest{
		From: req.From, To: req.To, Amount: amount, Asset: req.Token.Symbol, Token: req.Token,
	}, tronEnergyStrict)
	if err != nil {
		return nil, err
	}
	fixedFee := new(big.Int)
	for _, part := range []*big.Int{sweepPlan.quote.BandwidthFee, sweepPlan.quote.ActivationFee} {
		if part != nil {
			fixedFee.Add(fixedFee, part)
		}
	}
	required := a.requiredSweepFunding(ctx, params, req.Token.Contract, sweepPlan.quote.Energy, fixedFee, sweepPlan.quote.Fee())

	result := make([]types.UnsignedTx, 0, 2)
	held := new(big.Int)
	if req.NativeBalance != nil {
		held.Set(req.NativeBalance)
	}
	if held.Cmp(required) < 0 {
		seedAmount := new(big.Int).Sub(required, held)
		seedPlan, err := a.planNativeTransfer(ctx, params, req.To, req.From, seedAmount, true)
		if err != nil {
			return nil, fmt.Errorf("plan gas_seed: %w", err)
		}
		seedTx, err := a.buildFromPlan(ctx, seedPlan)
		if err != nil {
			return nil, fmt.Errorf("build gas_seed: %w", err)
		}
		result = append(result, *seedTx)
	}
	sweepTx, err := a.buildFromPlan(ctx, sweepPlan)
	if err != nil {
		return nil, err
	}
	sweepTx.Metadata[tronSweepMetaEnergy] = sweepPlan.quote.Energy
	sweepTx.Metadata[tronSweepMetaFixedFee] = fixedFee.String()
	sweepTx.Metadata[tronSweepMetaRequired] = required.String()
	return append(result, *sweepTx), nil
}

func (a *TronLive) buildNativeSweep(ctx context.Context, params tronChainParams, req types.SweepRequest) ([]types.UnsignedTx, error) {
	if req.NativeBalance == nil || req.NativeBalance.Sign() <= 0 {
		return nil, fmt.Errorf("native balance required for native sweep")
	}
	sized, err := a.planNativeTransfer(ctx, params, req.From, req.To, req.NativeBalance, true)
	if err != nil {
		return nil, err
	}
	fee := sized.quote.Fee()
	amount := new(big.Int).Sub(req.NativeBalance, fee)
	if amount.Sign() <= 0 {
		return nil, fmt.Errorf("insufficient native for sweep: balance=%s fee=%s", req.NativeBalance, fee)
	}
	plan, err := a.planNativeTransfer(ctx, params, req.From, req.To, amount, true)
	if err != nil {
		return nil, err
	}
	unsigned, err := a.buildFromPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	return []types.UnsignedTx{*unsigned}, nil
}

// ---------------------------------------------------------------------------
// Broadcast
// ---------------------------------------------------------------------------

// BroadcastTransaction submits the serialized signed transaction with
// /wallet/broadcasthex and returns its txID.
func (a *TronLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return "", fmt.Errorf("signed transaction is required")
	}
	rawBytes, signatures, err := decodeTronTransaction(signed.RawBytes)
	if err != nil {
		return "", err
	}
	if len(signatures) != 1 {
		return "", fmt.Errorf("tron broadcast: transaction carries %d signatures, want 1", len(signatures))
	}
	txID := hex.EncodeToString(tronTxID(rawBytes))
	if signed.TxHash != "" && !strings.EqualFold(signed.TxHash, txID) {
		return "", fmt.Errorf("tron broadcast: tx hash %s does not match txID %s", signed.TxHash, txID)
	}
	var response struct {
		tronCallResult
		TxID string `json:"txid"`
	}
	if err := a.post(ctx, "/wallet/broadcasthex", map[string]any{"transaction": hex.EncodeToString(signed.RawBytes)}, &response); err != nil {
		return "", err
	}
	if !response.Result {
		return "", fmt.Errorf("tron broadcast %s refused: %s", txID, response.describe())
	}
	if response.TxID != "" && !strings.EqualFold(response.TxID, txID) {
		return "", fmt.Errorf("tron broadcast: node returned txid %s for %s", response.TxID, txID)
	}
	return txID, nil
}
