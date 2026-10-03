package evmcall

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	blockLatest  = "latest"
	blockPending = "pending"
	statusOK     = "0x1"
)

// CallMsg is the call simulated with eth_call and eth_estimateGas.
type CallMsg struct {
	From  string
	To    string
	Value *big.Int
	Data  []byte
}

// Receipt is the part of a transaction receipt evm:call reports.
type Receipt struct {
	Status            string
	BlockNumber       uint64
	GasUsed           uint64
	EffectiveGasPrice *big.Int
}

// Succeeded reports status 0x1.
func (r *Receipt) Succeeded() bool {
	return r != nil && r.Status == statusOK
}

// RPC is the node surface evm:call needs.
type RPC interface {
	ChainID(ctx context.Context) (int64, error)
	Code(ctx context.Context, address string) ([]byte, error)
	Nonce(ctx context.Context, address string, block string) (uint64, error)
	Balance(ctx context.Context, address string) (*big.Int, error)
	GasPrice(ctx context.Context) (*big.Int, error)
	Call(ctx context.Context, msg CallMsg) ([]byte, error)
	EstimateGas(ctx context.Context, msg CallMsg) (uint64, error)
	SendRawTransaction(ctx context.Context, raw []byte) (string, error)
	TransactionKnown(ctx context.Context, hash string) (bool, error)
	Receipt(ctx context.Context, hash string) (*Receipt, error)
}

// JSONRPC implements RPC over chain.RPCClient, whose errors never carry the URL.
type JSONRPC struct {
	client *chain.RPCClient
}

func NewJSONRPC(url string) (*JSONRPC, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("rpc url must be http(s)")
	}
	return &JSONRPC{client: chain.NewRPCClient(url, "", "")}, nil
}

func (r *JSONRPC) quantity(ctx context.Context, method string, params ...interface{}) (*big.Int, error) {
	var result string
	if err := r.client.Call(ctx, method, &result, params...); err != nil {
		return nil, err
	}
	value, err := hexutil.DecodeBig(result)
	if err != nil {
		return nil, fmt.Errorf("%s returned %q: %w", method, result, err)
	}
	return value, nil
}

func (r *JSONRPC) uint64Quantity(ctx context.Context, method string, params ...interface{}) (uint64, error) {
	value, err := r.quantity(ctx, method, params...)
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("%s returned %s, out of range", method, value)
	}
	return value.Uint64(), nil
}

func (r *JSONRPC) ChainID(ctx context.Context) (int64, error) {
	value, err := r.quantity(ctx, "eth_chainId")
	if err != nil {
		return 0, err
	}
	if !value.IsInt64() {
		return 0, fmt.Errorf("eth_chainId returned %s, out of range", value)
	}
	return value.Int64(), nil
}

func (r *JSONRPC) Code(ctx context.Context, address string) ([]byte, error) {
	var result string
	if err := r.client.Call(ctx, "eth_getCode", &result, address, blockLatest); err != nil {
		return nil, err
	}
	return decodeHexBytes("eth_getCode", result)
}

func (r *JSONRPC) Nonce(ctx context.Context, address string, block string) (uint64, error) {
	return r.uint64Quantity(ctx, "eth_getTransactionCount", address, block)
}

func (r *JSONRPC) Balance(ctx context.Context, address string) (*big.Int, error) {
	return r.quantity(ctx, "eth_getBalance", address, blockLatest)
}

func (r *JSONRPC) GasPrice(ctx context.Context) (*big.Int, error) {
	return r.quantity(ctx, "eth_gasPrice")
}

func (r *JSONRPC) Call(ctx context.Context, msg CallMsg) ([]byte, error) {
	var result string
	if err := r.client.Call(ctx, "eth_call", &result, callObject(msg), blockLatest); err != nil {
		return nil, err
	}
	return decodeHexBytes("eth_call", result)
}

func (r *JSONRPC) EstimateGas(ctx context.Context, msg CallMsg) (uint64, error) {
	return r.uint64Quantity(ctx, "eth_estimateGas", callObject(msg))
}

func (r *JSONRPC) SendRawTransaction(ctx context.Context, raw []byte) (string, error) {
	var hash string
	if err := r.client.Call(ctx, "eth_sendRawTransaction", &hash, hexPrefix+hex.EncodeToString(raw)); err != nil {
		return "", err
	}
	return hash, nil
}

func (r *JSONRPC) TransactionKnown(ctx context.Context, hash string) (bool, error) {
	var result map[string]interface{}
	if err := r.client.Call(ctx, "eth_getTransactionByHash", &result, hash); err != nil {
		return false, err
	}
	return result != nil, nil
}

func (r *JSONRPC) Receipt(ctx context.Context, hash string) (*Receipt, error) {
	var raw *struct {
		Status            string `json:"status"`
		BlockNumber       string `json:"blockNumber"`
		GasUsed           string `json:"gasUsed"`
		EffectiveGasPrice string `json:"effectiveGasPrice"`
	}
	if err := r.client.Call(ctx, "eth_getTransactionReceipt", &raw, hash); err != nil {
		return nil, err
	}
	if raw == nil || raw.BlockNumber == "" {
		return nil, nil
	}
	block, err := hexutil.DecodeUint64(raw.BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("receipt block %q: %w", raw.BlockNumber, err)
	}
	gasUsed, err := hexutil.DecodeUint64(raw.GasUsed)
	if err != nil {
		return nil, fmt.Errorf("receipt gasUsed %q: %w", raw.GasUsed, err)
	}
	receipt := &Receipt{Status: raw.Status, BlockNumber: block, GasUsed: gasUsed}
	if price, err := hexutil.DecodeBig(raw.EffectiveGasPrice); err == nil {
		receipt.EffectiveGasPrice = price
	}
	return receipt, nil
}

func callObject(msg CallMsg) map[string]string {
	value := msg.Value
	if value == nil {
		value = new(big.Int)
	}
	return map[string]string{
		"from":  msg.From,
		"to":    msg.To,
		"value": hexutil.EncodeBig(value),
		"data":  hexPrefix + hex.EncodeToString(msg.Data),
	}
}

func decodeHexBytes(method, value string) ([]byte, error) {
	trimmed := strings.TrimPrefix(value, hexPrefix)
	if trimmed == "" || trimmed == "0" {
		return nil, nil
	}
	if len(trimmed)%2 == 1 {
		trimmed = "0" + trimmed
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s returned non-hex data", method)
	}
	return decoded, nil
}
