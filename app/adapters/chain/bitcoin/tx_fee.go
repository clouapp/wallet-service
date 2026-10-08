package bitcoin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
)

func coinToSats(coins decimal.Decimal) (*big.Int, error) {
	sats, err := amount.DecimalToBaseUnits(coins.String(), 8)
	if err != nil {
		return nil, fmt.Errorf("amount %s: %w", coins.String(), err)
	}
	return sats, nil
}

// TransactionFee is inputs minus outputs, through the provider failover.

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
		return 0, fmt.Errorf("btc tx %s: %w", txID, chain.ErrTransactionFeeUnknown)
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
	sats, err := coinToSats(in.Sub(out))
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
		var rpcErr *rpc.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == bitcoindTxNotFoundCode {
			return 0, fmt.Errorf("btc tx %s: %w", txID, chain.ErrTransactionFeeUnknown)
		}
		return 0, err
	}
	if tx.Fee != nil {
		sats, err := coinToSats(*tx.Fee)
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
	return rpcOutcome(p.live.rpcTransactionFee(ctx, txID))
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
