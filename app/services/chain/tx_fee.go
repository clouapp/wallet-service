package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

// TransactionFeeReader is an adapter that reads the fee a transaction it broadcast
// actually paid, in base units of the chain's native asset: what transactions.fee
// stores for withdrawals, sweeps and gas seeds once they confirm.
type TransactionFeeReader interface {
	TransactionFee(ctx context.Context, txHash string) (*big.Int, error)
}

// ErrTransactionFeeUnknown is a transaction the chain does not (yet) report.
var ErrTransactionFeeUnknown = errors.New("transaction fee not available")

func parseHexQuantity(value, field string) (*big.Int, error) {
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "0x"), "0X")
	if digits == "" {
		return nil, fmt.Errorf("%s is empty", field)
	}
	n, ok := new(big.Int).SetString(digits, 16)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("%s %q is not a hex quantity", field, value)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// EVM: gasUsed × effectiveGasPrice, plus the L1 data fee OP Stack chains (Base)
// report as l1Fee. Arbitrum folds its L1 cost into gasUsed.
// ---------------------------------------------------------------------------

type evmFeeReceipt struct {
	GasUsed           string `json:"gasUsed"`
	EffectiveGasPrice string `json:"effectiveGasPrice"`
	L1Fee             string `json:"l1Fee"`
}

func (a *EVMLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	var receipt *evmFeeReceipt
	if err := a.rpc.Call(ctx, "eth_getTransactionReceipt", &receipt, txHash); err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, ErrTransactionFeeUnknown)
	}
	gasUsed, err := parseHexQuantity(receipt.GasUsed, "gasUsed")
	if err != nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
	}
	priceHex := receipt.EffectiveGasPrice
	if priceHex == "" {
		// Receipts before London carry no effectiveGasPrice: the tx's gasPrice was paid.
		var tx *struct {
			GasPrice string `json:"gasPrice"`
		}
		if err := a.rpc.Call(ctx, "eth_getTransactionByHash", &tx, txHash); err != nil {
			return nil, err
		}
		if tx == nil {
			return nil, fmt.Errorf("evm transaction %s: %w", txHash, ErrTransactionFeeUnknown)
		}
		priceHex = tx.GasPrice
	}
	price, err := parseHexQuantity(priceHex, "gas price")
	if err != nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
	}
	fee := new(big.Int).Mul(gasUsed, price)
	if receipt.L1Fee != "" {
		l1Fee, err := parseHexQuantity(receipt.L1Fee, "l1Fee")
		if err != nil {
			return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
		}
		fee.Add(fee, l1Fee)
	}
	return fee, nil
}

// ---------------------------------------------------------------------------
// Solana: meta.fee (lamports) of the finalized transaction.
// ---------------------------------------------------------------------------

func (a *SolanaLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	if txHash == "" {
		return nil, fmt.Errorf("sol transaction signature is required")
	}
	var tx *struct {
		Meta *struct {
			Fee uint64 `json:"fee"`
		} `json:"meta"`
	}
	if err := a.rpc.Call(ctx, "getTransaction", &tx, txHash, map[string]any{
		"encoding": "json", "commitment": solanaCommitmentFinalized, "maxSupportedTransactionVersion": 0,
	}); err != nil {
		return nil, err
	}
	if tx == nil || tx.Meta == nil {
		return nil, fmt.Errorf("sol transaction %s: %w", txHash, ErrTransactionFeeUnknown)
	}
	return new(big.Int).SetUint64(tx.Meta.Fee), nil
}

// ---------------------------------------------------------------------------
// TRON: the sun burned, gettransactioninfobyid "fee" (energy + bandwidth + account
// activation); 0 when free bandwidth and staked energy covered the transaction.
// ---------------------------------------------------------------------------

type tronFeeInfo struct {
	ID      string `json:"id"`
	Fee     int64  `json:"fee"`
	Receipt struct {
		EnergyFee int64 `json:"energy_fee"`
		NetFee    int64 `json:"net_fee"`
	} `json:"receipt"`
}

func (a *TronLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	txID, err := normalizeTronTxID(txHash)
	if err != nil {
		return nil, err
	}
	var info tronFeeInfo
	if err := a.post(ctx, "/wallet/gettransactioninfobyid", map[string]any{"value": txID}, &info); err != nil {
		return nil, err
	}
	if info.ID == "" {
		return nil, fmt.Errorf("tron transaction %s: %w", txID, ErrTransactionFeeUnknown)
	}
	if !strings.EqualFold(info.ID, txID) {
		return nil, fmt.Errorf("tron gettransactioninfobyid %s answered for %s", txID, info.ID)
	}
	fee := max(info.Fee, info.Receipt.EnergyFee+info.Receipt.NetFee)
	if fee < 0 {
		return nil, fmt.Errorf("tron transaction %s reports a negative fee %d", txID, fee)
	}
	return big.NewInt(fee), nil
}

// ---------------------------------------------------------------------------
// Bitcoin family: inputs − outputs, through the provider failover.
// ---------------------------------------------------------------------------

func (a *BitcoinLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	txID, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return nil, err
	}
	fee, err := failoverRead(ctx, a.chainProviders(), "tx fee", func(p bitcoinProvider) (int64, error) { return p.transactionFee(ctx, txID) })
	if err != nil {
		return nil, err
	}
	return big.NewInt(fee), nil
}

// esploraTransactionFee reads the fee Esplora computes in GET /tx/:txid.
func (a *BitcoinLive) esploraTransactionFee(ctx context.Context, txID string) (int64, error) {
	body, err := a.esploraGet(ctx, "/tx/"+txID)
	if isEsploraNotFound(err) {
		return 0, fmt.Errorf("btc tx %s: %w", txID, ErrTransactionFeeUnknown)
	}
	if err != nil {
		return 0, err
	}
	var tx struct {
		Fee *int64 `json:"fee"`
	}
	if err := json.Unmarshal(body, &tx); err != nil {
		return 0, fmt.Errorf("parse tx %s: %w", txID, err)
	}
	if tx.Fee == nil || *tx.Fee < 0 {
		return 0, fmt.Errorf("btc tx %s has no fee", txID)
	}
	return *tx.Fee, nil
}

// btcVerboseTx is the part of a verbose transaction (bitcoind getrawtransaction,
// ElectrumX blockchain.transaction.get) the fee is computed from. Coinbase inputs
// have no txid.
type btcVerboseTx struct {
	Fee *decimal.Decimal `json:"fee"`
	Vin []struct {
		TxID string `json:"txid"`
		Vout uint32 `json:"vout"`
	} `json:"vin"`
	Vout []struct {
		Value decimal.Decimal `json:"value"`
		N     uint32          `json:"n"`
	} `json:"vout"`
}

func (tx btcVerboseTx) outputValue(n uint32) (decimal.Decimal, bool) {
	for _, out := range tx.Vout {
		if out.N == n {
			return out.Value, true
		}
	}
	return decimal.Zero, false
}

// feeFromPrevouts is Σ inputs − Σ outputs in satoshis; prev fetches a spent
// transaction verbose.
func feeFromPrevouts(txID string, tx btcVerboseTx, prev func(txID string) (btcVerboseTx, error)) (int64, error) {
	in := decimal.Zero
	for _, input := range tx.Vin {
		if input.TxID == "" {
			return 0, fmt.Errorf("btc tx %s is a coinbase: it pays no fee", txID)
		}
		spent, err := prev(input.TxID)
		if err != nil {
			return 0, fmt.Errorf("btc tx %s input %s:%d: %w", txID, input.TxID, input.Vout, err)
		}
		value, ok := spent.outputValue(input.Vout)
		if !ok {
			return 0, fmt.Errorf("btc tx %s input %s:%d: no such output", txID, input.TxID, input.Vout)
		}
		in = in.Add(value)
	}
	out := decimal.Zero
	for _, output := range tx.Vout {
		out = out.Add(output.Value)
	}
	sats, err := btcToSats(in.Sub(out))
	if err != nil {
		return 0, fmt.Errorf("btc tx %s fee: %w", txID, err)
	}
	if sats.Sign() < 0 || !sats.IsInt64() {
		return 0, fmt.Errorf("btc tx %s: inputs %s below outputs %s", txID, in, out)
	}
	return sats.Int64(), nil
}

// getrawtransaction verbosity: 1 is the decoded transaction, 2 adds "fee" (Core 25+,
// with the block's undo data).
const (
	bitcoindVerbose              = 1
	bitcoindVerbosityWithPrevout = 2
)

func (a *BitcoinLive) rpcTransactionFee(ctx context.Context, txID string) (int64, error) {
	var tx btcVerboseTx
	if err := a.rpc.Call(ctx, "getrawtransaction", &tx, txID, bitcoindVerbosityWithPrevout); err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && rpcErr.Code == bitcoindTxNotFoundCode {
			return 0, fmt.Errorf("btc tx %s: %w", txID, ErrTransactionFeeUnknown)
		}
		return 0, err
	}
	if tx.Fee != nil {
		sats, err := btcToSats(*tx.Fee)
		if err != nil || sats.Sign() < 0 || !sats.IsInt64() {
			return 0, fmt.Errorf("btc tx %s: fee %s is not usable", txID, tx.Fee)
		}
		return sats.Int64(), nil
	}
	return feeFromPrevouts(txID, tx, func(prevID string) (btcVerboseTx, error) {
		var prev btcVerboseTx
		err := a.rpc.Call(ctx, "getrawtransaction", &prev, prevID, bitcoindVerbose)
		return prev, err
	})
}

func (p directProvider) transactionFee(ctx context.Context, txID string) (int64, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return 0, err
	}
	if p.live.restAPI {
		return p.live.esploraTransactionFee(ctx, txID)
	}
	return p.live.rpcTransactionFee(ctx, txID)
}

func (p *electrumProvider) transactionFee(ctx context.Context, txID string) (int64, error) {
	return withSession(ctx, p, func(s *electrumSession) (int64, error) {
		get := func(id string) (btcVerboseTx, error) {
			var tx btcVerboseTx
			err := s.call("blockchain.transaction.get", &tx, id, true)
			return tx, err
		}
		tx, err := get(txID)
		if err != nil {
			return 0, err
		}
		return feeFromPrevouts(txID, tx, get)
	})
}
