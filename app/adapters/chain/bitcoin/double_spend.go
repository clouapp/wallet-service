package bitcoin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	_ chain.SpentInputsReader   = (*BitcoinLive)(nil)
	_ chain.InputsSpendReader   = (*BitcoinLive)(nil)
	_ chain.TransactionSearcher = (*BitcoinLive)(nil)
)

// btcOutpoint is one output a transaction spends.
type btcOutpoint struct {
	txID string
	vout uint32
}

// btcOutpointSpend is what a provider says about one outpoint: the transaction that
// spent it, in block (0 while that transaction is unconfirmed).
type btcOutpointSpend struct {
	spent bool
	txID  string
	block uint64
}

// btcSpendsView is the tip and the spends of some outpoints, read from one provider.
type btcSpendsView struct {
	tip    uint64
	spends []btcOutpointSpend
}

// SpentInputs are the outpoints ("txid:vout") the signed transaction spends, in
// input order.
func (a *BitcoinLive) SpentInputs(signed *types.SignedTx) ([]string, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return nil, fmt.Errorf("btc spent inputs: signed transaction is required")
	}
	var msg wire.MsgTx
	if err := msg.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		return nil, fmt.Errorf("btc spent inputs: decode the signed transaction: %w", err)
	}
	inputs := make([]string, 0, len(msg.TxIn))
	for _, in := range msg.TxIn {
		inputs = append(inputs, in.PreviousOutPoint.Hash.String()+":"+strconv.FormatUint(uint64(in.PreviousOutPoint.Index), 10))
	}
	return inputs, nil
}

// SearchTransactionBlock asks every provider, in order and cooling down or not, for
// the block of txHash: the first that has it in a block answers. 0 when the
// providers that answered do not have it in a block; an error only when none
// answered.
func (a *BitcoinLive) SearchTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	txID, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return 0, err
	}
	return a.chainProviders().searchTransactionBlock(ctx, txID)
}

// btcDoubleSpendDepth is the default depth a conflicting spend must reach before it
// proves a transfer can never land (BitcoinConfig.DoubleSpendDepth overrides it).
const btcDoubleSpendDepth = 6

// InputsSpend reads, from the first provider that can say (an Esplora indexer),
// which transaction spent each input of query.TxHash, and the tip. An input spent by
// the transfer itself, or by a transaction paying query.To exactly query.Amount (a
// malleated or re-signed copy), is Ours (in its block, or in the mempool). Every
// input spent by other transactions paying someone else, each at least the
// double-spend depth deep (the larger of the chain's confirmations and
// DoubleSpendDepth, 6 by default), is Conflicted: the transfer can never land.
// Anything else (an input unspent, a spend in the mempool or not deep enough)
// proves nothing; a conflicting transaction whose outputs cannot be read is an
// error.
func (a *BitcoinLive) InputsSpend(ctx context.Context, query chain.InputsQuery) (chain.InputsSpend, error) {
	ours, err := normalizeBTCHash(query.TxHash, "txid")
	if err != nil {
		return chain.InputsSpend{}, err
	}
	if len(query.Inputs) == 0 {
		return chain.InputsSpend{}, fmt.Errorf("btc inputs spend: the transaction's inputs are required")
	}
	outpoints := make([]btcOutpoint, 0, len(query.Inputs))
	for _, input := range query.Inputs {
		outpoint, parseErr := parseBTCOutpoint(input)
		if parseErr != nil {
			return chain.InputsSpend{}, parseErr
		}
		outpoints = append(outpoints, outpoint)
	}
	providers := a.chainProviders()
	view, err := providers.outpointSpends(ctx, outpoints)
	if err != nil {
		return chain.InputsSpend{}, err
	}
	copies := map[string]bool{}
	isOurs := func(spend btcOutpointSpend) (bool, error) {
		if spend.txID == ours {
			return true, nil
		}
		if known, ok := copies[spend.txID]; ok {
			return known, nil
		}
		outputs, outputsErr := providers.transactionOutputs(ctx, spend.txID)
		if outputsErr != nil {
			return false, fmt.Errorf("btc inputs spend: outputs of the conflicting %s: %w", spend.txID, outputsErr)
		}
		copies[spend.txID] = paysTo(outputs, query.To, query.Amount)
		return copies[spend.txID], nil
	}
	depth := max(a.cfg.Confirmations, a.doubleSpendDepth())
	conflict := chain.InputsSpend{Conflicted: true}
	for _, spend := range view.spends {
		if !spend.spent {
			conflict.Conflicted = false
			continue
		}
		mine, oursErr := isOurs(spend)
		if oursErr != nil {
			return chain.InputsSpend{}, oursErr
		}
		if mine {
			return chain.InputsSpend{Ours: true, Block: spend.block, TxHash: spend.txID}, nil
		}
		deep := spend.block > 0 && view.tip >= spend.block && view.tip-spend.block+1 >= depth
		if !deep {
			conflict.Conflicted = false
			continue
		}
		conflict.Block = max(conflict.Block, spend.block)
	}
	if !conflict.Conflicted {
		return chain.InputsSpend{}, nil
	}
	return conflict, nil
}

func (a *BitcoinLive) doubleSpendDepth() uint64 {
	if a.cfg.DoubleSpendDepth > 0 {
		return a.cfg.DoubleSpendDepth
	}
	return btcDoubleSpendDepth
}

// paysTo reports outputs holding one that pays to exactly amount.
func paysTo(outputs []btcOutput, to string, amount *big.Int) bool {
	to = strings.TrimSpace(to)
	if to == "" || amount == nil || !amount.IsInt64() {
		return false
	}
	for _, output := range outputs {
		if strings.EqualFold(strings.TrimSpace(output.Address), to) && output.Value == amount.Int64() {
			return true
		}
	}
	return false
}

func parseBTCOutpoint(input string) (btcOutpoint, error) {
	txID, voutText, ok := strings.Cut(strings.TrimSpace(input), ":")
	hash, err := normalizeBTCHash(txID, "input txid")
	if !ok || err != nil {
		return btcOutpoint{}, fmt.Errorf("btc input %q is not txid:vout", input)
	}
	vout, err := strconv.ParseUint(voutText, 10, 32)
	if err != nil {
		return btcOutpoint{}, fmt.Errorf("btc input %q is not txid:vout", input)
	}
	return btcOutpoint{txID: hash, vout: uint32(vout)}, nil
}

// searchTransactionBlock is SearchTransactionBlock on every member.
func (f *bitcoinFailover) searchTransactionBlock(ctx context.Context, txID string) (uint64, error) {
	var failures []error
	answered := false
	for _, member := range f.members {
		block, err := member.provider.transactionBlock(ctx, txID)
		switch {
		case err == nil && block > 0:
			member.succeeded()
			return block, nil
		case err == nil:
			member.succeeded()
			answered = true
		default:
			failures = append(failures, fmt.Errorf("%s: %w", member.provider.label(), err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	if answered {
		return 0, nil
	}
	return 0, fmt.Errorf("btc tx search: no provider answered: %w", errors.Join(failures...))
}

func (f *bitcoinFailover) outpointSpends(ctx context.Context, outpoints []btcOutpoint) (btcSpendsView, error) {
	return failoverRead(ctx, f, "outspends", func(p bitcoinProvider) (btcSpendsView, error) { return p.outpointSpends(ctx, outpoints) })
}

func (f *bitcoinFailover) transactionOutputs(ctx context.Context, txID string) ([]btcOutput, error) {
	return failoverRead(ctx, f, "tx outputs", func(p bitcoinProvider) ([]btcOutput, error) { return p.transactionOutputs(ctx, txID) })
}

func (p directProvider) transactionOutputs(ctx context.Context, txID string) ([]btcOutput, error) {
	if !p.live.restAPI {
		return nil, errProviderUnsupported
	}
	if err := p.verifyNetwork(ctx); err != nil {
		return nil, err
	}
	return p.live.transactionOutputsREST(ctx, txID)
}

// transactionOutputs is unsupported: only Esplora is asked who spent an input.
func (p *electrumProvider) transactionOutputs(context.Context, string) ([]btcOutput, error) {
	return nil, errProviderUnsupported
}

// transactionOutputs is unsupported: only Esplora is asked who spent an input.
func (p tatumProvider) transactionOutputs(context.Context, string) ([]btcOutput, error) {
	return nil, errProviderUnsupported
}

// transactionOutputsREST reads GET /tx/:txid: the address and value of each output.
func (a *BitcoinLive) transactionOutputsREST(ctx context.Context, txID string) ([]btcOutput, error) {
	body, err := a.esploraGet(ctx, "/tx/"+txID)
	if err != nil {
		return nil, fmt.Errorf("fetch tx %s: %w", txID, err)
	}
	var tx esploraTx
	if err := json.Unmarshal(body, &tx); err != nil {
		return nil, fmt.Errorf("parse tx %s: %w", txID, err)
	}
	outputs := make([]btcOutput, 0, len(tx.Vout))
	for _, out := range tx.Vout {
		outputs = append(outputs, btcOutput{Address: out.ScriptpubkeyAddress, Value: out.Value})
	}
	return outputs, nil
}

func (p directProvider) outpointSpends(ctx context.Context, outpoints []btcOutpoint) (btcSpendsView, error) {
	if !p.live.restAPI {
		// bitcoind keeps no index of who spent an output.
		return btcSpendsView{}, errProviderUnsupported
	}
	if err := p.verifyNetwork(ctx); err != nil {
		return btcSpendsView{}, err
	}
	return p.live.outpointSpendsREST(ctx, outpoints)
}

// outpointSpends is unsupported: the Electrum protocol has no outpoint lookup.
func (p *electrumProvider) outpointSpends(context.Context, []btcOutpoint) (btcSpendsView, error) {
	return btcSpendsView{}, errProviderUnsupported
}

// outpointSpends is unsupported: the Tatum gateway is a bitcoind without a spend index.
func (p tatumProvider) outpointSpends(context.Context, []btcOutpoint) (btcSpendsView, error) {
	return btcSpendsView{}, errProviderUnsupported
}

// esploraOutspend is GET /tx/:txid/outspend/:vout. An output Esplora does not know
// answers unspent.
type esploraOutspend struct {
	Spent  bool            `json:"spent"`
	TxID   string          `json:"txid"`
	Status esploraTxStatus `json:"status"`
}

// outpointSpendsREST reads the tip, then each outpoint's spend.
func (a *BitcoinLive) outpointSpendsREST(ctx context.Context, outpoints []btcOutpoint) (btcSpendsView, error) {
	tip, err := a.getLatestBlockREST(ctx)
	if err != nil {
		return btcSpendsView{}, err
	}
	view := btcSpendsView{tip: tip, spends: make([]btcOutpointSpend, 0, len(outpoints))}
	for _, outpoint := range outpoints {
		path := "/tx/" + outpoint.txID + "/outspend/" + strconv.FormatUint(uint64(outpoint.vout), 10)
		body, err := a.esploraGet(ctx, path)
		if err != nil {
			return btcSpendsView{}, fmt.Errorf("fetch the spend of %s:%d: %w", outpoint.txID, outpoint.vout, err)
		}
		var answer esploraOutspend
		if parseErr := json.Unmarshal(body, &answer); parseErr != nil {
			return btcSpendsView{}, fmt.Errorf("parse the spend of %s:%d: %w", outpoint.txID, outpoint.vout, parseErr)
		}
		spend := btcOutpointSpend{spent: answer.Spent}
		if answer.Spent {
			if spend.txID, err = normalizeBTCHash(answer.TxID, "spending txid"); err != nil {
				return btcSpendsView{}, err
			}
			if answer.Status.Confirmed {
				if answer.Status.BlockHeight == nil || *answer.Status.BlockHeight == 0 {
					return btcSpendsView{}, fmt.Errorf("the spend of %s:%d is confirmed without a block height", outpoint.txID, outpoint.vout)
				}
				spend.block = *answer.Status.BlockHeight
			}
		}
		view.spends = append(view.spends, spend)
	}
	return view, nil
}
