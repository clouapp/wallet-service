package bitcoin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// InputsSpend reads, from the first provider that can say (an Esplora indexer),
// which transaction spent each input of txHash, and the tip. An input spent by
// txHash itself is Ours (in its block, or in the mempool); every input spent by
// another transaction at least the chain's confirmations deep is Conflicted: txHash
// can never land. Anything else (an input unspent, a spend in the mempool or not
// deep enough) proves nothing.
func (a *BitcoinLive) InputsSpend(ctx context.Context, txHash string, inputs []string) (chain.InputsSpend, error) {
	ours, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return chain.InputsSpend{}, err
	}
	if len(inputs) == 0 {
		return chain.InputsSpend{}, fmt.Errorf("btc inputs spend: the transaction's inputs are required")
	}
	outpoints := make([]btcOutpoint, 0, len(inputs))
	for _, input := range inputs {
		outpoint, parseErr := parseBTCOutpoint(input)
		if parseErr != nil {
			return chain.InputsSpend{}, parseErr
		}
		outpoints = append(outpoints, outpoint)
	}
	view, err := a.chainProviders().outpointSpends(ctx, outpoints)
	if err != nil {
		return chain.InputsSpend{}, err
	}
	depth := max(a.cfg.Confirmations, 1)
	conflict := chain.InputsSpend{Conflicted: true}
	for _, spend := range view.spends {
		if spend.spent && spend.txID == ours {
			return chain.InputsSpend{Ours: true, Block: spend.block}, nil
		}
		deep := spend.spent && spend.block > 0 && view.tip >= spend.block && view.tip-spend.block+1 >= depth
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
