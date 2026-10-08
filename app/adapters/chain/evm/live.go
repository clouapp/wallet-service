package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

const evmGasPriceMultiplier = int64(2)

const (
	evmNativeTransferGasLimit = uint64(21_000)
	// evmERC20TransferGasFloor is the lowest limit ever encoded for a token
	// transfer, even when eth_estimateGas reports less.
	evmERC20TransferGasFloor = uint64(65_000)
	// evmERC20GasMarginPercent pads eth_estimateGas: proxied tokens (e.g. Circle
	// USDC) and first-time recipients cost more than a plain ERC-20 transfer.
	evmERC20GasMarginPercent = uint64(125)
	// evmGasSeedBufferPercent sizes a gas_seed above the child's token sweep fee.
	evmGasSeedBufferPercent = int64(120)
	percentDenominator      = 100
)

// ---------------------------------------------------------------------------
// EVMConfig — all that differs between EVM chains.
// ETH, Polygon, Arbitrum, Base, etc. = same adapter, different config. The fee
// model (L1 data fee on Base, L1 gas on Arbitrum) follows NetworkID.
// ---------------------------------------------------------------------------

type EVMConfig struct {
	ChainIDStr    string
	ChainName     string
	NativeSymbol  string
	NativeDecimal uint8
	NetworkID     int64
	RPCURL        string
	Confirmations uint64
	// ERC20Tokens lists registered ERC-20s for deposit log matching (from DB at boot).
	ERC20Tokens []types.Token
	// GasReadinessThreshold is the minimum native balance BaseAddress must hold to
	// be considered gas-ready. Populated from the chains table. nil when unset.
	GasReadinessThreshold *big.Int
	// DustThresholdNative is the minimum native balance on a child for sweep
	// eligibility. Populated from the chains table. nil when unset.
	DustThresholdNative *big.Int
	// StrictLogScan fails ScanBlock when eth_getLogs fails, so the deposit scanner
	// retries the block instead of marking it scanned without its ERC-20 deposits.
	StrictLogScan bool
}

// EVMLive talks to an EVM JSON-RPC node.
// The chain service keeps the types.Chain port this client already satisfies.
type EVMLive struct {
	cfg EVMConfig
	rpc *rpc.RPCClient
	fee chain.FeePolicy
}

var (
	_ types.Chain             = (*EVMLive)(nil)
	_ chain.FeePolicyScoped   = (*EVMLive)(nil)
	_ chain.FeePolicyReporter = (*EVMLive)(nil)
	_ chain.EVMCallBuilder    = (*EVMLive)(nil)
)

// WithFeePolicy is this adapter bidding gas prices scaled by policy; it shares
// the RPC client.
func (a *EVMLive) WithFeePolicy(policy chain.FeePolicy) types.Chain {
	return &EVMLive{cfg: a.cfg, rpc: a.rpc, fee: policy}
}

// FeePolicy is the wallet fee policy this adapter prices with.
func (a *EVMLive) FeePolicy() chain.FeePolicy { return a.fee }

// evmRPCMaxResponseBytes bounds one JSON-RPC answer: eth_getBlockByNumber with full
// transactions on a busy L2 block is several MB (Base Sepolia 47639340 is ~4.3 MB),
// and L2 gas limits allow far larger calldata blocks than the 1 MiB default.
const evmRPCMaxResponseBytes = 64 << 20

func NewEVMLive(cfg EVMConfig) *EVMLive {
	return &EVMLive{
		cfg: cfg,
		rpc: rpc.NewRPCClient(rpc.RPCClientDeps{URL: cfg.RPCURL}).WithMaxResponseBytes(evmRPCMaxResponseBytes),
	}
}

// Endpoint is the URL the next dial uses. Callers must not log it.
func (a *EVMLive) Endpoint() string {
	if a == nil || a.rpc == nil {
		return ""
	}
	return a.rpc.Endpoint()
}

// ReplaceEndpoint points later dials at endpoint and leaves gas and dust
// thresholds unchanged. An empty value does not wipe the current endpoint.
func (a *EVMLive) ReplaceEndpoint(endpoint string) {
	if a == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	a.cfg.RPCURL = endpoint
	if a.rpc == nil {
		a.rpc = rpc.NewRPCClient(rpc.RPCClientDeps{URL: endpoint})
		return
	}
	a.rpc.ReplaceEndpoint(endpoint)
}

func (a *EVMLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *EVMLive) Name() string                  { return a.cfg.ChainName }
func (a *EVMLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *EVMLive) NativeAsset() string           { return a.cfg.NativeSymbol }

// NativeDecimals is the chain row's native_decimals for amounts leaving this adapter.
func (a *EVMLive) NativeDecimals() int {
	if a == nil {
		return 0
	}
	return int(a.cfg.NativeDecimal)
}

func (a *EVMLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	// TODO: BIP-44 m/44'/60'/0'/0/{index} via hdkeychain
	return "", fmt.Errorf("EVM key derivation not implemented — use go-ethereum/crypto + hdkeychain")
}

func (a *EVMLive) ValidateAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	for _, c := range address[2:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (a *EVMLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	var hexBal string
	if err := a.rpc.Call(ctx, "eth_getBalance", &hexBal, address, "latest"); err != nil {
		return nil, err
	}
	bal := hexToBigInt(hexBal)
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: bal, Decimals: a.cfg.NativeDecimal, Human: fmtUnits(bal, a.cfg.NativeDecimal)}, nil
}

func (a *EVMLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	data := "0x70a08231" + padAddr(address) // balanceOf(address)
	var hexResult string
	if err := a.rpc.Call(ctx, "eth_call", &hexResult, map[string]string{"to": token.Contract, "data": data}, "latest"); err != nil {
		return nil, err
	}
	bal := hexToBigInt(hexResult)
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: bal, Decimals: token.Decimals, Human: fmtUnits(bal, token.Decimals)}, nil
}

func (a *EVMLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	gasLimit, err := a.EstimateTransferGasLimit(ctx, req)
	if err != nil {
		return nil, err
	}

	var hexNonce string
	if err := a.rpc.Call(ctx, "eth_getTransactionCount", &hexNonce, req.From, "pending"); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	// Prefer a caller-supplied gas price so callers that size amounts against a
	// specific price (e.g. BuildSweep) encode the tx with the same price.
	gasPrice := req.GasPrice
	if gasPrice == nil {
		suggested, err := a.EstimateGasPrice(ctx)
		if err != nil {
			return nil, err
		}
		gasPrice = suggested
	}

	var txData []byte
	to := req.To
	value := req.Amount

	if req.Token != nil {
		txData = encodeERC20Transfer(req.To, req.Amount)
		to = req.Token.Contract
		value = big.NewInt(0)
	}

	unsigned := &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"nonce":     hexToUint64(hexNonce),
			"to":        to,
			"value":     value.String(),
			"gas_limit": gasLimit,
			"gas_price": gasPrice.String(),
			"chain_id":  a.cfg.NetworkID,
			"data":      txData,
		},
	}
	if req.Amount != nil {
		unsigned.TransferAmount = new(big.Int).Set(req.Amount)
	}
	transaction, signer, err := a.transactionFromUnsigned(unsigned)
	if err != nil {
		return nil, err
	}
	unsigned.RawBytes = signer.Hash(transaction).Bytes()
	return unsigned, nil
}

func (a *EVMLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	gasPrice, err := a.EstimateGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	gasLimit, err := a.EstimateTransferGasLimit(ctx, req)
	if err != nil {
		return nil, err
	}

	fee, err := a.transferFee(ctx, req, gasLimit, gasPrice)
	if err != nil {
		return nil, err
	}

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, a.cfg.NativeDecimal),
		FeeAsset: a.cfg.NativeSymbol,
		GasPrice: gasPrice.String(),
		GasLimit: gasLimit,
	}, nil
}

// EstimateTransferGasLimit returns the gas limit BuildTransfer encodes for req.
// A caller-supplied req.GasLimit wins; native transfers use NativeTransferGasLimit
// (the fixed transfer limit except on Arbitrum); token transfers simulate
// transfer(to, amount) from req.From with
// eth_estimateGas, add evmERC20GasMarginPercent and never go below
// evmERC20TransferGasFloor. A failed or zero estimate returns
// ErrGasEstimateFailed instead of guessing, so the caller cannot broadcast.
func (a *EVMLive) EstimateTransferGasLimit(ctx context.Context, req types.TransferRequest) (uint64, error) {
	if req.GasLimit != nil && *req.GasLimit > 0 {
		return *req.GasLimit, nil
	}
	if req.Token == nil {
		return a.NativeTransferGasLimit(ctx, req.From, req.To)
	}
	if err := validateTokenTransferForEstimate(req); err != nil {
		return 0, err
	}

	call := map[string]string{
		"from": req.From,
		"to":   req.Token.Contract,
		"data": "0x" + hex.EncodeToString(encodeERC20Transfer(req.To, req.Amount)),
	}
	var hexEstimate string
	if err := a.rpc.Call(ctx, "eth_estimateGas", &hexEstimate, call); err != nil {
		return 0, fmt.Errorf("%w: %s transfer of %s from %s to %s: %w",
			chain.ErrGasEstimateFailed, req.Token.Symbol, req.Amount, req.From, req.To, err)
	}
	estimate := hexToUint64(hexEstimate)
	if estimate == 0 {
		return 0, fmt.Errorf("%w: node returned no gas for %s transfer from %s",
			chain.ErrGasEstimateFailed, req.Token.Symbol, req.From)
	}

	padded := estimate * evmERC20GasMarginPercent / percentDenominator
	if padded < evmERC20TransferGasFloor {
		return evmERC20TransferGasFloor, nil
	}
	return padded, nil
}

func validateTokenTransferForEstimate(req types.TransferRequest) error {
	switch {
	case req.From == "":
		return fmt.Errorf("%w: %s transfer has no sender", chain.ErrGasEstimateFailed, req.Token.Symbol)
	case req.To == "":
		return fmt.Errorf("%w: %s transfer has no recipient", chain.ErrGasEstimateFailed, req.Token.Symbol)
	case req.Amount == nil || req.Amount.Sign() <= 0:
		return fmt.Errorf("%w: %s transfer has no positive amount", chain.ErrGasEstimateFailed, req.Token.Symbol)
	}
	return nil
}

// BuildSweep builds the txs to move `asset` from req.From to req.To inside the same wallet.
// For native (req.Token == nil): 1 tx sending (native_balance - gas_reserve) to req.To.
// For ERC-20 tokens:
//   - If req.NativeBalance has enough for the token transfer's gas: 1 tx (child → Base token.transfer)
//   - Else: 2 txs — gas_seed (Base → child native) + token sweep (child → Base token.transfer)
func (a *EVMLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	gasPrice, err := a.EstimateGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	if req.Token == nil {
		if req.NativeBalance == nil {
			return nil, fmt.Errorf("native balance required for native sweep")
		}
		nativeReq := types.TransferRequest{From: req.From, To: req.To, Asset: a.cfg.NativeSymbol, GasPrice: gasPrice}
		gasLimit, err := a.NativeTransferGasLimit(ctx, req.From, req.To)
		if err != nil {
			return nil, err
		}
		feeReserve, err := a.transferFee(ctx, nativeReq, gasLimit, gasPrice)
		if err != nil {
			return nil, err
		}
		amount := new(big.Int).Sub(req.NativeBalance, feeReserve)
		if amount.Sign() <= 0 {
			return nil, chain.Insufficient(fmt.Errorf("insufficient native for sweep: balance=%s fee=%s", req.NativeBalance, feeReserve))
		}
		nativeReq.Amount = amount
		nativeReq.GasLimit = &gasLimit
		unsigned, err := a.BuildTransfer(ctx, nativeReq)
		if err != nil {
			return nil, err
		}
		return []types.UnsignedTx{*unsigned}, nil
	}

	amount := req.Amount
	if amount == nil {
		bal, err := a.GetTokenBalance(ctx, req.From, *req.Token)
		if err != nil {
			return nil, fmt.Errorf("get token balance: %w", err)
		}
		amount = bal.Amount
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("no token balance to sweep")
	}

	sweepReq := types.TransferRequest{
		From: req.From, To: req.To, Amount: amount, Asset: req.Token.Symbol, Token: req.Token,
		GasPrice: gasPrice,
	}
	sweepGasLimit, err := a.EstimateTransferGasLimit(ctx, sweepReq)
	if err != nil {
		return nil, err
	}
	sweepReq.GasLimit = &sweepGasLimit
	feeNeeded, err := a.transferFee(ctx, sweepReq, sweepGasLimit, gasPrice)
	if err != nil {
		return nil, err
	}

	result := make([]types.UnsignedTx, 0, 2)
	if req.NativeBalance == nil || req.NativeBalance.Cmp(feeNeeded) < 0 {
		seedAmount := new(big.Int).Mul(feeNeeded, big.NewInt(evmGasSeedBufferPercent))
		seedAmount.Div(seedAmount, big.NewInt(percentDenominator))
		seedTx, err := a.BuildTransfer(ctx, types.TransferRequest{
			From: req.To, To: req.From, Amount: seedAmount, Asset: a.cfg.NativeSymbol,
			GasPrice: gasPrice,
		})
		if err != nil {
			return nil, fmt.Errorf("build gas_seed: %w", err)
		}
		result = append(result, *seedTx)
	}

	sweepTx, err := a.BuildTransfer(ctx, sweepReq)
	if err != nil {
		return nil, err
	}
	result = append(result, *sweepTx)
	return result, nil
}

// EstimateGasPrice is the gas price every transaction this adapter builds bids,
// in wei: evmGasPriceMultiplier × eth_gasPrice, scaled by the wallet's fee
// policy. The planner, the fee quote and BuildTransfer / BuildSweep all price
// with it, so an estimate and the broadcast transaction use the same formula.
func (a *EVMLive) EstimateGasPrice(ctx context.Context) (*big.Int, error) {
	var hexGas string
	if err := a.rpc.Call(ctx, "eth_gasPrice", &hexGas); err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}
	return a.fee.ScaleGasPrice(bufferedEVMGasPrice(hexToBigInt(hexGas))), nil
}

// NativeTransferReserve is the most a native transfer built now can spend on fees:
// BuildTransfer encodes a legacy tx with the native gas limit at the buffered gas
// price (EstimateGasPrice), so the fee is limit × price, plus the buffered L1 data
// fee on OP-stack networks. EVM accounts keep no minimum balance. The sweep planner
// adds the fee to native withdrawals and subtracts it from native sweeps.
func (a *EVMLive) NativeTransferReserve(ctx context.Context) (fee, minimumRemaining *big.Int, err error) {
	gasPrice, err := a.EstimateGasPrice(ctx)
	if err != nil {
		return nil, nil, err
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return nil, nil, fmt.Errorf("gas price: node returned no usable gas price")
	}
	gasLimit, err := a.NativeTransferGasLimit(ctx, "", "")
	if err != nil {
		return nil, nil, err
	}
	fee, err = a.transferFee(ctx, types.TransferRequest{GasPrice: gasPrice}, gasLimit, gasPrice)
	if err != nil {
		return nil, nil, err
	}
	return fee, new(big.Int), nil
}

func bufferedEVMGasPrice(suggested *big.Int) *big.Int {
	if suggested == nil || suggested.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Mul(new(big.Int).Set(suggested), big.NewInt(evmGasPriceMultiplier))
}

// GasReadinessThreshold returns the minimum native balance on BaseAddress for the
// wallet to be considered gas-ready. Value is sourced from the chains table via
// EVMConfig. Returns nil when unset.
func (a *EVMLive) GasReadinessThreshold() *big.Int {
	return a.cfg.GasReadinessThreshold
}

// DustThreshold returns the minimum balance a child must hold for sweep eligibility.
// For the native asset, returns cfg.DustThresholdNative. For tokens, returns nil in
// v1 — USD→raw conversion happens in sweep.Planner using the price service.
func (a *EVMLive) DustThreshold(asset string) *big.Int {
	if asset == a.cfg.NativeSymbol {
		return a.cfg.DustThresholdNative
	}
	return nil
}

// SignTransaction is required by the chain interface. EVM signing lives in the
// custody service, which asks mpc for the signature and then calls
// FinalizeMPCSignature. This method does not use the private key.
func (a *EVMLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, _ []byte) (*types.SignedTx, error) {
	return nil, fmt.Errorf("EVM signing not implemented — use go-ethereum/types.SignTx")
}

func (a *EVMLive) FinalizeMPCSignature(
	unsigned *types.UnsignedTx,
	signature []byte,
	publicKey []byte,
) (*types.SignedTx, error) {
	if unsigned == nil {
		return nil, fmt.Errorf("unsigned transaction is required")
	}
	if len(signature) != 64 {
		return nil, fmt.Errorf("MPC signature must contain exactly 64 R/S bytes")
	}
	if len(publicKey) != 33 && len(publicKey) != 65 {
		return nil, fmt.Errorf("EVM public key must contain 33 or 65 bytes")
	}

	transaction, signer, err := a.transactionFromUnsigned(unsigned)
	if err != nil {
		return nil, err
	}
	signingHash := signer.Hash(transaction).Bytes()
	if len(unsigned.RawBytes) != 32 || !bytes.Equal(unsigned.RawBytes, signingHash) {
		return nil, fmt.Errorf("unsigned transaction signing hash mismatch")
	}

	expectedPublicKey, err := normalizeEVMCompressedPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	signatureWithRecovery, err := recoverEVMRecoveryID(signingHash, signature, expectedPublicKey)
	if err != nil {
		return nil, err
	}
	signedTransaction, err := transaction.WithSignature(signer, signatureWithRecovery)
	if err != nil {
		return nil, fmt.Errorf("attach MPC signature: %w", err)
	}
	rawTransaction, err := signedTransaction.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("serialize signed EVM transaction: %w", err)
	}
	return &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: rawTransaction, TxHash: signedTransaction.Hash().Hex()}, nil
}

func (a *EVMLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return "", fmt.Errorf("signed transaction is required")
	}
	rawHex := "0x" + hex.EncodeToString(signed.RawBytes)
	var txHash string
	if err := a.rpc.Call(ctx, "eth_sendRawTransaction", &txHash, rawHex); err != nil {
		return "", chain.ClassifyBroadcast(err)
	}
	return txHash, nil
}

func (a *EVMLive) transactionFromUnsigned(
	unsigned *types.UnsignedTx,
) (*gethtypes.Transaction, gethtypes.Signer, error) {
	if unsigned == nil || unsigned.Metadata == nil {
		return nil, nil, fmt.Errorf("unsigned EVM transaction metadata is required")
	}
	metadata := unsigned.Metadata

	nonce, ok := metadata["nonce"].(uint64)
	if !ok {
		return nil, nil, fmt.Errorf("unsigned EVM transaction nonce is invalid")
	}
	toAddress, ok := metadata["to"].(string)
	if !ok || !common.IsHexAddress(toAddress) {
		return nil, nil, fmt.Errorf("unsigned EVM transaction destination is invalid")
	}
	value, err := metadataBigInt(metadata, "value", true)
	if err != nil {
		return nil, nil, err
	}
	gasPrice, err := metadataBigInt(metadata, "gas_price", false)
	if err != nil {
		return nil, nil, err
	}
	gasLimit, ok := metadata["gas_limit"].(uint64)
	if !ok || gasLimit == 0 {
		return nil, nil, fmt.Errorf("unsigned EVM transaction gas limit is invalid")
	}
	data, ok := metadata["data"].([]byte)
	if !ok && metadata["data"] != nil {
		return nil, nil, fmt.Errorf("unsigned EVM transaction data is invalid")
	}
	chainID, err := metadataInt64(metadata, "chain_id")
	if err != nil || chainID <= 0 {
		return nil, nil, fmt.Errorf("unsigned EVM transaction chain id is invalid")
	}
	if a.cfg.NetworkID > 0 && chainID != a.cfg.NetworkID {
		return nil, nil, fmt.Errorf(
			"unsigned EVM transaction chain id %d does not match adapter chain id %d",
			chainID,
			a.cfg.NetworkID,
		)
	}

	transaction := gethtypes.NewTransaction(
		nonce,
		common.HexToAddress(toAddress),
		value,
		gasLimit,
		gasPrice,
		data,
	)
	signer := gethtypes.LatestSignerForChainID(big.NewInt(chainID))
	return transaction, signer, nil
}

func metadataBigInt(metadata map[string]interface{}, field string, allowZero bool) (*big.Int, error) {
	value, ok := metadata[field].(string)
	if !ok || value == "" {
		return nil, fmt.Errorf("unsigned EVM transaction %s is invalid", field)
	}
	result, ok := new(big.Int).SetString(value, 10)
	if !ok || result.Sign() < 0 || (!allowZero && result.Sign() == 0) {
		return nil, fmt.Errorf("unsigned EVM transaction %s is invalid", field)
	}
	return result, nil
}

func metadataInt64(metadata map[string]interface{}, field string) (int64, error) {
	switch value := metadata[field].(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("%s overflows int64", field)
		}
		return int64(value), nil
	default:
		return 0, fmt.Errorf("%s is not an integer", field)
	}
}

func normalizeEVMCompressedPublicKey(publicKey []byte) ([]byte, error) {
	if len(publicKey) == 33 {
		if _, err := crypto.DecompressPubkey(publicKey); err != nil {
			return nil, fmt.Errorf("invalid compressed EVM public key: %w", err)
		}
		return append([]byte(nil), publicKey...), nil
	}
	parsed, err := crypto.UnmarshalPubkey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid EVM public key: %w", err)
	}
	return crypto.CompressPubkey(parsed), nil
}

func recoverEVMRecoveryID(hash, signature, expectedCompressedPublicKey []byte) ([]byte, error) {
	for recoveryID := byte(0); recoveryID <= 1; recoveryID++ {
		candidate := make([]byte, 65)
		copy(candidate, signature)
		candidate[64] = recoveryID
		recovered, err := crypto.SigToPub(hash, candidate)
		if err != nil {
			continue
		}
		if bytes.Equal(crypto.CompressPubkey(recovered), expectedCompressedPublicKey) {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("MPC signature does not match wallet public key")
}

func (a *EVMLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	var hexBlock string
	if err := a.rpc.Call(ctx, "eth_blockNumber", &hexBlock); err != nil {
		return 0, err
	}
	return hexToUint64(hexBlock), nil
}

// GetTransactionBlock returns the block number that included `txHash`, or 0 when
// the node reports the tx as still pending (blockNumber is null or the tx is
// unknown). The confirmation loop uses this to backfill BlockNumber on sweep /
// withdrawal / gas_seed rows — those are inserted immediately after
// broadcasting and therefore carry block_number=0 until mined. A missing tx is
// treated as "still pending" so the next tick can retry without the caller
// having to distinguish between "not mined yet" and "dropped"; truly dropped
// txs are handled separately when they eventually stop appearing.
func (a *EVMLive) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	var tx *struct {
		BlockNumber string `json:"blockNumber"`
	}
	if err := a.rpc.Call(ctx, "eth_getTransactionByHash", &tx, txHash); err != nil {
		return 0, err
	}
	if tx == nil || tx.BlockNumber == "" {
		return 0, nil
	}
	return hexToUint64(tx.BlockNumber), nil
}

func (a *EVMLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	hexBlock := fmt.Sprintf("0x%x", blockNum)
	var block struct {
		Hash         string `json:"hash"`
		Timestamp    string `json:"timestamp"`
		Transactions []struct {
			Hash  string `json:"hash"`
			From  string `json:"from"`
			To    string `json:"to"`
			Value string `json:"value"`
		} `json:"transactions"`
	}
	if err := a.rpc.Call(ctx, "eth_getBlockByNumber", &block, hexBlock, true); err != nil {
		return nil, err
	}

	blockTime := time.Unix(int64(hexToUint64(block.Timestamp)), 0)
	var transfers []types.DetectedTransfer

	// Native transfers
	for _, tx := range block.Transactions {
		val := hexToBigInt(tx.Value)
		if val.Sign() > 0 {
			transfers = append(transfers, types.DetectedTransfer{
				TxHash: tx.Hash, BlockNumber: blockNum, BlockHash: block.Hash,
				From: tx.From, To: tx.To, Amount: val,
				Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
			})
		}
	}

	// ERC-20 Transfer events
	tokens, err := a.scanERC20(ctx, blockNum, block.Hash, blockTime)
	if err != nil && a.cfg.StrictLogScan {
		return nil, fmt.Errorf("erc20 logs of block %d: %w", blockNum, err)
	}
	transfers = append(transfers, tokens...)

	return transfers, nil
}

func (a *EVMLive) scanERC20(ctx context.Context, blockNum uint64, blockHash string, blockTime time.Time) ([]types.DetectedTransfer, error) {
	// Caller must have registered tokens in the registry — we access via package-level
	// In production, inject registry into adapter. For POC, accept this coupling.
	transferTopic := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	hexBlock := fmt.Sprintf("0x%x", blockNum)

	var logs []struct {
		Address         string   `json:"address"`
		Topics          []string `json:"topics"`
		Data            string   `json:"data"`
		TransactionHash string   `json:"transactionHash"`
		LogIndex        string   `json:"logIndex"`
	}

	// Single getLogs call for the entire block, no address filter
	// We'll match against known token contracts in Go
	if err := a.rpc.Call(ctx, "eth_getLogs", &logs, map[string]interface{}{
		"fromBlock": hexBlock, "toBlock": hexBlock,
		"topics": []string{transferTopic},
	}); err != nil {
		return nil, err
	}

	var result []types.DetectedTransfer
	for _, log := range logs {
		if len(log.Topics) < 3 {
			continue
		}
		from := topicToAddr(log.Topics[1])
		to := topicToAddr(log.Topics[2])
		amount := hexToBigInt(log.Data)

		// Match against known tokens — O(n) but n is small (2-4 tokens per chain)
		contractLower := strings.ToLower(log.Address)
		for _, t := range a.cfg.ERC20Tokens {
			if t.ChainID == a.cfg.ChainIDStr && strings.ToLower(t.Contract) == contractLower {
				tokenCopy := t
				result = append(result, types.DetectedTransfer{
					TxHash: log.TransactionHash, BlockNumber: blockNum, BlockHash: blockHash,
					From: from, To: to, Amount: amount,
					Asset: t.Symbol, Token: &tokenCopy,
					LogIndex: uint(hexToUint64(log.LogIndex)), Timestamp: blockTime,
				})
				break
			}
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Shared EVM helpers
// ---------------------------------------------------------------------------

var erc20Selector = []byte{0xa9, 0x05, 0x9c, 0xbb} // transfer(address,uint256)

func encodeERC20Transfer(to string, amount *big.Int) []byte {
	data := make([]byte, 68)
	copy(data[0:4], erc20Selector)
	addr, _ := hex.DecodeString(strings.TrimPrefix(to, "0x"))
	copy(data[16:36], addr)
	amtBytes := amount.Bytes()
	copy(data[68-len(amtBytes):68], amtBytes)
	return data
}

func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	if s == "" || s == "0" {
		return big.NewInt(0)
	}
	n := new(big.Int)
	n.SetString(s, 16)
	return n
}

func hexToUint64(s string) uint64 {
	return hexToBigInt(s).Uint64()
}

func padAddr(addr string) string {
	return fmt.Sprintf("%064s", strings.TrimPrefix(strings.ToLower(addr), "0x"))
}

func topicToAddr(topic string) string {
	t := strings.TrimPrefix(topic, "0x")
	if len(t) >= 40 {
		return "0x" + t[len(t)-40:]
	}
	return "0x" + t
}

func fmtUnits(units *big.Int, decimals uint8) string {
	if units == nil {
		return "0"
	}
	return amount.FormatBaseUnits(units, int(decimals))
}
