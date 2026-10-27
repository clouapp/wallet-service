package withdrawrecoveryruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"regexp"
	"strings"

	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/types"
)

var hashPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
var hexPattern = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)

type rpcFacts struct {
	network                                int64
	latest, pending                        uint64
	native, tokens                         string
	decimals                               int
	originalKnown, candidateKnown, receipt bool
}

func hexInt(value string) (*big.Int, error) {
	if !hexPattern.MatchString(value) {
		return nil, refuse("RPC returned an invalid hexadecimal integer")
	}
	n, ok := new(big.Int).SetString(value[2:], 16)
	if !ok || n.BitLen() > 256 {
		return nil, refuse("RPC integer exceeds bound")
	}
	return n, nil
}
func callHex(ctx context.Context, rpc RPC, method string, args ...interface{}) (*big.Int, error) {
	var value string
	if rpc.Call(ctx, method, &value, args...) != nil {
		return nil, refuse("independent RPC read unavailable")
	}
	return hexInt(value)
}
func known(ctx context.Context, rpc RPC, method, hash string) (bool, error) {
	var raw json.RawMessage
	if rpc.Call(ctx, method, &raw, hash) != nil {
		return false, refuse("transaction evidence unavailable")
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return false, nil
	}
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return false, refuse("transaction evidence malformed")
	}
	key := "hash"
	if method == "eth_getTransactionReceipt" {
		key = "transactionHash"
	}
	var returned string
	if json.Unmarshal(object[key], &returned) != nil || !strings.EqualFold(returned, hash) {
		return false, refuse("transaction evidence hash differs")
	}
	return true, nil
}
func readRPC(ctx context.Context, rpc RPC, b recovery.Binding, candidate string) (rpcFacts, error) {
	var f rpcFacts
	network, err := callHex(ctx, rpc, "eth_chainId")
	if err != nil || !network.IsInt64() {
		return f, refuse("RPC network unavailable")
	}
	f.network = network.Int64()
	// Refuse a foreign network before asking it for account or transaction facts.
	if f.network != b.NetworkID {
		return f, refuse("RPC network differs")
	}
	n, err := callHex(ctx, rpc, "eth_getTransactionCount", b.SourceAddress, "latest")
	if err != nil || !n.IsUint64() {
		return f, refuse("latest nonce unavailable")
	}
	f.latest = n.Uint64()
	n, err = callHex(ctx, rpc, "eth_getTransactionCount", b.SourceAddress, "pending")
	if err != nil || !n.IsUint64() {
		return f, refuse("pending nonce unavailable")
	}
	f.pending = n.Uint64()
	f.originalKnown, err = known(ctx, rpc, "eth_getTransactionByHash", b.OriginalTxHash)
	if err != nil {
		return f, err
	}
	f.receipt, err = known(ctx, rpc, "eth_getTransactionReceipt", b.OriginalTxHash)
	if err != nil {
		return f, err
	}
	if candidate != "" {
		f.candidateKnown, err = known(ctx, rpc, "eth_getTransactionByHash", candidate)
		if err != nil {
			return f, err
		}
		receipt, receiptErr := known(ctx, rpc, "eth_getTransactionReceipt", candidate)
		if receiptErr != nil {
			return f, receiptErr
		}
		f.receipt = f.receipt || receipt
	}
	n, err = callHex(ctx, rpc, "eth_getBalance", b.SourceAddress, "latest")
	if err != nil {
		return f, err
	}
	f.native = n.String()
	n, err = callHex(ctx, rpc, "eth_call", map[string]string{"to": b.TokenContract, "data": "0x70a08231" + strings.Repeat("0", 24) + strings.TrimPrefix(b.SourceAddress, "0x")}, "latest")
	if err != nil {
		return f, err
	}
	f.tokens = n.String()
	n, err = callHex(ctx, rpc, "eth_call", map[string]string{"to": b.TokenContract, "data": "0x313ce567"}, "latest")
	if err != nil || !n.IsInt64() || n.Int64() > 36 {
		return f, refuse("token precision unavailable")
	}
	f.decimals = int(n.Int64())
	return f, nil
}
func quoteFee(ctx context.Context, adapter types.Chain, b recovery.Binding, token *types.Token) (recovery.FeeQuote, error) {
	var q recovery.FeeQuote
	estimator, ok := adapter.(types.TransferGasEstimator)
	if !ok {
		return q, refuse("exact transfer gas estimator unavailable")
	}
	units, err := positive(b.AmountRaw)
	if err != nil {
		return q, err
	}
	req := types.TransferRequest{From: b.SourceAddress, To: b.Recipient, Amount: units, Asset: b.Asset, Token: token}
	limit, err := estimator.EstimateTransferGasLimit(ctx, req)
	if err != nil || limit == 0 {
		return q, refuse("exact child transfer gas quote unavailable")
	}
	price, err := adapter.EstimateGasPrice(ctx)
	if err != nil || price == nil || price.Sign() <= 0 || price.BitLen() > 256 {
		return q, refuse("wallet gas price unavailable")
	}
	req.GasLimit = &limit
	req.GasPrice = price
	l1 := new(big.Int)
	if estimator, ok := adapter.(types.L1DataFeeEstimator); ok {
		l1, err = estimator.EstimateL1DataFee(ctx, req)
		if err != nil || l1 == nil || l1.Sign() < 0 || l1.BitLen() > 256 {
			return q, refuse("exact L1 data fee quote unavailable")
		}
	}
	return recovery.FeeQuote{GasLimit: limit, GasPriceWei: price.String(), L1FeeWei: l1.String()}, nil
}
