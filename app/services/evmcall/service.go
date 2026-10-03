package evmcall

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	defaultReceiptPoll    = 10 * time.Second
	defaultReceiptTimeout = 15 * time.Minute

	OutcomeBroadcast       = "broadcast"
	OutcomeSendFailed      = "send failed; nothing was broadcast"
	OutcomeSendErrorKnown  = "send reported an error but the node knows the transaction"
	OutcomeHashMismatch    = "node returned a different hash than the signed one"
	OutcomeNoReceiptYet    = "broadcast; no receipt before the wait ended (check on chain before any resend)"
	OutcomeReceiptSuccess  = "success"
	OutcomeReceiptReverted = "reverted"
)

var (
	ErrPendingTransactions = errors.New("source has pending transactions")
	ErrSimulationFailed    = errors.New("simulation failed; nothing signed")
	ErrNotSent             = errors.New("transaction was not broadcast")
)

// WalletSource loads the wallet whose base address pays.
type WalletSource interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// Signer MPC-signs a built call from the wallet's base address and verifies it
// (sweep.EVMCallPreflighter). It never broadcasts.
type Signer interface {
	PreflightEVMCall(ctx context.Context, walletID uuid.UUID, passphrase string, adapter types.Chain, unsigned *types.UnsignedTx) (*types.SignedTx, error)
}

type Dependencies struct {
	RPC     RPC
	Wallets WalletSource
	Signer  Signer
	Claimer Claimer
}

type Service struct {
	deps           Dependencies
	receiptPoll    time.Duration
	receiptTimeout time.Duration
	sleep          func(ctx context.Context, d time.Duration) error
}

func NewService(deps Dependencies) (*Service, error) {
	if deps.RPC == nil || deps.Wallets == nil {
		return nil, fmt.Errorf("evm call: rpc and wallet source are required")
	}
	return &Service{deps: deps, receiptPoll: defaultReceiptPoll, receiptTimeout: defaultReceiptTimeout, sleep: sleepContext}, nil
}

// Plan is the simulated call: what would be signed, at which nonce and cost.
type Plan struct {
	Network     string `json:"network"`
	ChainID     int64  `json:"chain_id"`
	WalletID    string `json:"wallet_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	ValueWei    string `json:"value_wei"`
	Value       string `json:"value"`
	Data        string `json:"data"`
	Nonce       uint64 `json:"nonce"`
	GasEstimate uint64 `json:"gas_estimate"`
	GasLimit    uint64 `json:"gas_limit"`
	GasPriceWei string `json:"gas_price_wei"`
	MaxFeeWei   string `json:"max_fee_wei"`
	BalanceWei  string `json:"balance_wei"`
	CallResult  string `json:"eth_call_result"`
	gasPrice    *big.Int
}

// Result is a broadcast: the plan, the hash, and what the chain said.
type Result struct {
	Plan         *Plan    `json:"plan"`
	Tag          string   `json:"tag"`
	SignedTxHash string   `json:"signed_tx_hash"`
	TxHash       string   `json:"tx_hash"`
	Outcome      string   `json:"outcome"`
	SendError    string   `json:"send_error,omitempty"`
	ClaimPath    string   `json:"claim_path"`
	ResultPath   string   `json:"result_path,omitempty"`
	Receipt      *Receipt `json:"receipt,omitempty"`
	FeeWei       string   `json:"fee_wei,omitempty"`
	RecordedAt   string   `json:"recorded_at"`
}

// Simulate runs every check that needs no key: network, bytecode, nonce, balance,
// eth_call and eth_estimateGas. It never signs nor sends.
func (s *Service) Simulate(ctx context.Context, request Request) (*Plan, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	wallet, err := s.payingWallet(ctx, request.WalletID)
	if err != nil {
		return nil, err
	}
	return s.plan(ctx, request, wallet.DepositAddress.Address)
}

// Broadcast simulates, MPC-signs, claims the tag, and sends the transaction exactly
// once. It never retries; after a send it waits for the receipt and records it.
func (s *Service) Broadcast(ctx context.Context, request Request, passphrase string) (*Result, error) {
	if err := request.ValidateBroadcast(passphrase); err != nil {
		return nil, err
	}
	if s.deps.Signer == nil || s.deps.Claimer == nil {
		return nil, fmt.Errorf("evm call: signer and claimer are required to broadcast")
	}
	wallet, err := s.payingWallet(ctx, request.WalletID)
	if err != nil {
		return nil, err
	}
	plan, err := s.plan(ctx, request, wallet.DepositAddress.Address)
	if err != nil {
		return nil, err
	}
	signed, err := s.sign(ctx, wallet, request, plan, passphrase)
	if err != nil {
		return nil, err
	}

	result := &Result{Plan: plan, Tag: request.Tag, SignedTxHash: signed.TxHash}
	claimDetails, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode claim: %w", err)
	}
	if result.ClaimPath, err = s.deps.Claimer.Claim(request.Tag, claimDetails); err != nil {
		return nil, err
	}
	pending, err := s.deps.RPC.Nonce(ctx, plan.From, blockPending)
	if err != nil {
		return result, fmt.Errorf("%w: pending nonce lookup failed after signing: %v (claim %s kept)", ErrNotSent, err, result.ClaimPath)
	}
	if pending != plan.Nonce {
		return result, fmt.Errorf("%w: nonce of %s moved from %d to %d after signing (claim %s kept)", ErrNotSent, plan.From, plan.Nonce, pending, result.ClaimPath)
	}

	s.sendOnce(ctx, signed, result)
	s.record(result)
	if result.TxHash == "" {
		return result, fmt.Errorf("%w: %s", ErrNotSent, result.SendError)
	}
	if result.Outcome == OutcomeHashMismatch {
		return result, fmt.Errorf("%s: signed %s, node answered %s; check both on chain before anything else", OutcomeHashMismatch, result.SignedTxHash, result.TxHash)
	}
	s.awaitReceipt(ctx, result)
	s.record(result)
	return result, nil
}

func (s *Service) payingWallet(ctx context.Context, walletID uuid.UUID) (*models.Wallet, error) {
	if ctx == nil {
		return nil, fmt.Errorf("evm call: context is required")
	}
	wallet, err := s.deps.Wallets.FindByID(ctx, walletID)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("wallet %s not found", walletID)
	}
	if wallet.DepositAddress == nil || !common.IsHexAddress(wallet.DepositAddress.Address) {
		return nil, fmt.Errorf("wallet %s has no EVM base address", walletID)
	}
	if mpcpkg.Curve(wallet.MPCCurve) != mpcpkg.CurveSecp256k1 {
		return nil, fmt.Errorf("wallet %s uses %s; evm calls need secp256k1", walletID, wallet.MPCCurve)
	}
	return wallet, nil
}

func (s *Service) plan(ctx context.Context, request Request, from string) (*Plan, error) {
	network, err := TestnetNetwork(request.ChainID)
	if err != nil {
		return nil, err
	}
	rpc := s.deps.RPC
	served, err := rpc.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("eth_chainId: %w", err)
	}
	if served != request.ChainID {
		return nil, fmt.Errorf("rpc serves chain %d, not %d (%s)", served, request.ChainID, network.Name)
	}
	if err := s.requireCodeForCalldata(ctx, request); err != nil {
		return nil, err
	}
	nonce, err := s.requireNoPending(ctx, from)
	if err != nil {
		return nil, err
	}

	msg := CallMsg{From: from, To: request.To, Value: request.Value, Data: request.Data}
	callResult, err := rpc.Call(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("%w: eth_call: %v", ErrSimulationFailed, err)
	}
	estimate, err := rpc.EstimateGas(ctx, msg)
	if err != nil || estimate == 0 {
		return nil, fmt.Errorf("%w: eth_estimateGas: %v", ErrSimulationFailed, err)
	}
	gasLimit, err := chooseGasLimit(request.GasLimit, estimate)
	if err != nil {
		return nil, err
	}
	gasPrice, err := s.bufferedGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	maxFee := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
	balance, err := rpc.Balance(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("eth_getBalance: %w", err)
	}
	if needed := new(big.Int).Add(request.Value, maxFee); balance.Cmp(needed) < 0 {
		return nil, fmt.Errorf("balance %s wei is below value + max fee %s wei", balance, needed)
	}

	return &Plan{
		Network: network.Name, ChainID: request.ChainID, WalletID: request.WalletID.String(),
		From: from, To: request.To, ValueWei: request.Value.String(),
		Value: FormatNative(request.Value) + " " + network.NativeSymbol, Data: hexPrefix + hex.EncodeToString(request.Data),
		Nonce: nonce, GasEstimate: estimate, GasLimit: gasLimit, GasPriceWei: gasPrice.String(),
		MaxFeeWei: maxFee.String(), BalanceWei: balance.String(), CallResult: hexPrefix + hex.EncodeToString(callResult),
		gasPrice: gasPrice,
	}, nil
}

func (s *Service) requireCodeForCalldata(ctx context.Context, request Request) error {
	if len(request.Data) == 0 {
		return nil
	}
	code, err := s.deps.RPC.Code(ctx, request.To)
	if err != nil {
		return fmt.Errorf("eth_getCode: %w", err)
	}
	if len(code) == 0 {
		return fmt.Errorf("%s has no bytecode on chain %d; refusing to send calldata to it", request.To, request.ChainID)
	}
	return nil
}

func (s *Service) requireNoPending(ctx context.Context, from string) (uint64, error) {
	latest, err := s.deps.RPC.Nonce(ctx, from, blockLatest)
	if err != nil {
		return 0, fmt.Errorf("latest nonce: %w", err)
	}
	pending, err := s.deps.RPC.Nonce(ctx, from, blockPending)
	if err != nil {
		return 0, fmt.Errorf("pending nonce: %w", err)
	}
	if latest != pending {
		return 0, fmt.Errorf("%w: %s latest nonce %d, pending %d", ErrPendingTransactions, from, latest, pending)
	}
	return latest, nil
}

func (s *Service) bufferedGasPrice(ctx context.Context) (*big.Int, error) {
	suggested, err := s.deps.RPC.GasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("eth_gasPrice: %w", err)
	}
	if suggested == nil || suggested.Sign() <= 0 {
		return nil, fmt.Errorf("eth_gasPrice returned no usable price")
	}
	buffered := new(big.Int).Mul(suggested, big.NewInt(gasPriceMultiplier))
	if buffered.Cmp(maxGasPrice()) > 0 {
		return nil, fmt.Errorf("buffered gas price %s wei exceeds the %d wei cap", buffered, MaxGasPriceWei)
	}
	return buffered, nil
}

// chooseGasLimit pads the estimate unless the caller set a limit, which must cover it.
func chooseGasLimit(requested, estimate uint64) (uint64, error) {
	if requested > 0 {
		if requested < estimate {
			return 0, fmt.Errorf("gas limit %d is below the estimate %d", requested, estimate)
		}
		return requested, nil
	}
	padded := estimate * gasLimitMarginPercent / percentDenominator
	if padded > MaxGasLimit {
		return 0, fmt.Errorf("padded gas estimate %d exceeds %d", padded, MaxGasLimit)
	}
	return padded, nil
}

func (s *Service) sign(ctx context.Context, wallet *models.Wallet, request Request, plan *Plan, passphrase string) (*types.SignedTx, error) {
	adapter := chain.NewEVMLive(chain.EVMConfig{ChainIDStr: wallet.Chain, ChainName: plan.Network, NetworkID: request.ChainID})
	unsigned, err := adapter.BuildCall(chain.EVMCall{
		Nonce: plan.Nonce, To: request.To, Value: request.Value, Data: request.Data,
		GasLimit: plan.GasLimit, GasPrice: plan.gasPrice,
	})
	if err != nil {
		return nil, err
	}
	signed, err := s.deps.Signer.PreflightEVMCall(ctx, request.WalletID, passphrase, adapter, unsigned)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	if signed == nil || len(signed.RawBytes) == 0 || signed.TxHash == "" {
		return nil, fmt.Errorf("sign: signer returned no transaction")
	}
	return signed, nil
}

// sendOnce calls eth_sendRawTransaction exactly once and classifies the answer.
func (s *Service) sendOnce(ctx context.Context, signed *types.SignedTx, result *Result) {
	returned, err := s.deps.RPC.SendRawTransaction(ctx, signed.RawBytes)
	if err != nil {
		result.SendError = err.Error()
		if known, lookupErr := s.deps.RPC.TransactionKnown(ctx, signed.TxHash); lookupErr == nil && known {
			result.TxHash, result.Outcome = signed.TxHash, OutcomeSendErrorKnown
			return
		}
		result.Outcome = OutcomeSendFailed
		return
	}
	if !strings.EqualFold(returned, signed.TxHash) {
		result.TxHash, result.Outcome = returned, OutcomeHashMismatch
		return
	}
	result.TxHash, result.Outcome = signed.TxHash, OutcomeBroadcast
}

func (s *Service) awaitReceipt(ctx context.Context, result *Result) {
	deadline := time.Now().Add(s.receiptTimeout)
	for {
		receipt, err := s.deps.RPC.Receipt(ctx, result.TxHash)
		if err == nil && receipt != nil {
			result.Receipt = receipt
			result.Outcome = OutcomeReceiptReverted
			if receipt.Succeeded() {
				result.Outcome = OutcomeReceiptSuccess
			}
			result.FeeWei = receiptFee(receipt, result.Plan.gasPrice).String()
			return
		}
		if !time.Now().Before(deadline) || s.sleep(ctx, s.receiptPoll) != nil {
			result.Outcome = OutcomeNoReceiptYet
			return
		}
	}
}

func receiptFee(receipt *Receipt, signedGasPrice *big.Int) *big.Int {
	price := signedGasPrice
	if receipt.EffectiveGasPrice != nil && receipt.EffectiveGasPrice.Sign() > 0 {
		price = receipt.EffectiveGasPrice
	}
	return new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), price)
}

// record stores the outcome next to the claim; a failed write never hides the hash,
// which the caller prints either way.
func (s *Service) record(result *Result) {
	result.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return
	}
	if path, err := s.deps.Claimer.Record(result.Tag, encoded); err == nil {
		result.ResultPath = path
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
