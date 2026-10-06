package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/pkg/types"
)

// bitcoinProvider is one source of chain data and one broadcast path of a
// Bitcoin-family network. The adapter tries its providers in order through a
// bitcoinFailover; a provider that cannot serve an operation at all answers
// errProviderUnsupported and is skipped without being blamed.
type bitcoinProvider interface {
	// label names the provider in logs; it never carries the URL, which may embed a key.
	label() string
	balance(ctx context.Context, address string) (*types.Balance, error)
	confirmedUTXOs(ctx context.Context, address string) ([]btcInput, error)
	latestBlock(ctx context.Context) (uint64, error)
	scanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error)
	transactionBlock(ctx context.Context, txID string) (uint64, error)
	feeRate(ctx context.Context) (int64, error)
	// transactionFee is what txID paid, inputs minus outputs, in satoshis.
	transactionFee(ctx context.Context, txID string) (int64, error)
	broadcast(ctx context.Context, raw []byte) (string, error)
}

// errProviderUnsupported is a provider that cannot serve an operation (an Electrum
// server cannot list a block's transactions, a public node has no wallet for
// listunspent); the failover moves on without counting it as a failure.
var errProviderUnsupported = errors.New("operation not supported by this provider")

// jsonRPCMethodNotFoundCode is a node that does not expose a method (Tatum's gateway
// has no wallet for listunspent): the call is unsupported there, not a failure.
const jsonRPCMethodNotFoundCode = -32601

func unsupportedWhenMethodNotFound(err error) error {
	var rpcErr *rpc.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == jsonRPCMethodNotFoundCode {
		return fmt.Errorf("%w: %s", errProviderUnsupported, rpcErr.Message)
	}
	return err
}

func rpcOutcome[T any](value T, err error) (T, error) {
	return value, unsupportedWhenMethodNotFound(err)
}

// btcBroadcastRejectedError is a node that received the transaction and refused it
// under its rules (bad signature, missing inputs, fee below the relay minimum). Any
// other node would refuse the same bytes, so the failover does not try the next one.
type btcBroadcastRejectedError struct {
	provider string
	reason   string
}

func (e *btcBroadcastRejectedError) Error() string {
	return fmt.Sprintf("btc broadcast rejected by %s: %s", e.provider, e.reason)
}

// btcAlreadyKnownMarkers are the node answers to a transaction it already has in its
// mempool or chain (Bitcoin Core and Litecoin Core reject reasons, Esplora and
// ElectrumX pass them through). Re-sending the same signed bytes is idempotent, so
// these mean the broadcast succeeded.
var btcAlreadyKnownMarkers = []string{
	"txn-already-in-mempool",
	"txn-already-known",
	"transaction already in block chain",
	"transaction outputs already in utxo set",
}

// bitcoindAlreadyInChainCode is RPC_VERIFY_ALREADY_IN_CHAIN; -25 (verify error), -26
// (rejected) and -22 (decode failed) are refusals of the transaction itself.
const (
	bitcoindAlreadyInChainCode = -27
	bitcoindVerifyErrorCode    = -25
	bitcoindVerifyRejectedCode = -26
	bitcoindDecodeFailedCode   = -22
)

func isBTCAlreadyKnown(reason string) bool {
	lower := strings.ToLower(reason)
	for _, marker := range btcAlreadyKnownMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// btcTxID is the txid of a serialized transaction, or "" when raw does not decode.
func btcTxID(raw []byte) string {
	var msg wire.MsgTx
	if err := msg.Deserialize(bytes.NewReader(raw)); err != nil {
		return ""
	}
	return msg.TxHash().String()
}

// directProvider is one endpoint spoken to with the adapter's own transport: the
// Esplora REST API (Blockstream, mempool.space, Litecoin Space) or bitcoind JSON-RPC.
// The primary provider is the adapter itself; a fallback is a copy of it pointed at
// another URL, which first proves it serves the adapter's network.
type directProvider struct {
	live *BitcoinLive
	name string
	// kind replaces "esplora"/"json-rpc" in the label (a Tatum gateway).
	kind string
	// checkGenesis makes the first call verify the block at height 0 is the network's
	// genesis block, so a fallback configured for the wrong network is never used.
	checkGenesis    bool
	genesisVerified *atomic.Bool
}

func newDirectProvider(live *BitcoinLive, name string, checkGenesis bool) directProvider {
	return directProvider{live: live, name: name, checkGenesis: checkGenesis, genesisVerified: &atomic.Bool{}}
}

func (p directProvider) label() string {
	if p.kind != "" {
		return p.name + " (" + p.kind + ")"
	}
	if p.live.restAPI {
		return p.name + " (esplora)"
	}
	return p.name + " (json-rpc)"
}

func (p directProvider) balance(ctx context.Context, address string) (*types.Balance, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return nil, err
	}
	if p.live.restAPI {
		return p.live.getBalanceREST(ctx, address)
	}
	return rpcOutcome(p.live.getBalanceRPC(ctx, address))
}

func (p directProvider) confirmedUTXOs(ctx context.Context, address string) ([]btcInput, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return nil, err
	}
	if p.live.restAPI {
		return p.live.listUTXOsREST(ctx, address)
	}
	return rpcOutcome(p.live.listUTXOsRPC(ctx, address))
}

func (p directProvider) latestBlock(ctx context.Context) (uint64, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return 0, err
	}
	if p.live.restAPI {
		return p.live.getLatestBlockREST(ctx)
	}
	var count uint64
	if err := p.live.rpc.Call(ctx, "getblockcount", &count); err != nil {
		return 0, unsupportedWhenMethodNotFound(err)
	}
	return count, nil
}

func (p directProvider) scanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return nil, err
	}
	if p.live.restAPI {
		return p.live.scanBlockREST(ctx, blockNum)
	}
	return rpcOutcome(p.live.scanBlockRPC(ctx, blockNum))
}

func (p directProvider) transactionBlock(ctx context.Context, txID string) (uint64, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return 0, err
	}
	if p.live.restAPI {
		return p.live.getTransactionBlockREST(ctx, txID)
	}
	return rpcOutcome(p.live.getTransactionBlockRPC(ctx, txID))
}

func (p directProvider) feeRate(ctx context.Context) (int64, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return 0, err
	}
	if p.live.restAPI {
		return p.live.fetchEsploraFeeRate(ctx)
	}
	return rpcOutcome(p.live.fetchSmartFeeRate(ctx))
}

func (p directProvider) broadcast(ctx context.Context, raw []byte) (string, error) {
	if err := p.verifyNetwork(ctx); err != nil {
		return "", err
	}
	if p.live.restAPI {
		return p.broadcastREST(ctx, raw)
	}
	return p.broadcastRPC(ctx, raw)
}

// broadcastREST posts the hex to Esplora's POST /tx. A 4xx answer carries the node's
// reject reason; 5xx, gateway errors and transport failures mean it was not decided.
func (p directProvider) broadcastREST(ctx context.Context, raw []byte) (string, error) {
	status, body, err := p.live.esploraPost(ctx, "/tx", hex.EncodeToString(raw))
	if err != nil {
		return "", err
	}
	reason := strings.TrimSpace(string(body))
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return reason, nil
	}
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError && !isRateLimited(status, body) {
		return "", &btcBroadcastRejectedError{provider: p.label(), reason: fmt.Sprintf("HTTP %d: %s", status, reason)}
	}
	return "", &esploraStatusError{path: "/tx", status: status, body: reason}
}

func (p directProvider) broadcastRPC(ctx context.Context, raw []byte) (string, error) {
	var txHash string
	err := p.live.rpc.Call(ctx, "sendrawtransaction", &txHash, hex.EncodeToString(raw))
	var rpcErr *rpc.RPCError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case bitcoindAlreadyInChainCode:
			return "", &btcBroadcastRejectedError{provider: p.label(), reason: "transaction already in block chain: " + rpcErr.Message}
		case bitcoindVerifyErrorCode, bitcoindVerifyRejectedCode, bitcoindDecodeFailedCode:
			return "", &btcBroadcastRejectedError{provider: p.label(), reason: rpcErr.Error()}
		}
	}
	if err != nil {
		return "", unsupportedWhenMethodNotFound(err)
	}
	return txHash, nil
}

// verifyNetwork checks, once per fallback, that its genesis block is the network's.
func (p directProvider) verifyNetwork(ctx context.Context) error {
	want := p.live.network.genesisHash
	if !p.checkGenesis || want == "" || p.genesisVerified.Load() {
		return nil
	}
	got, err := p.genesisHash(ctx)
	if err != nil {
		return fmt.Errorf("%s: read genesis block: %w", p.label(), err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s serves genesis %s, not %s's %s", p.label(), got, p.live.network.name, want)
	}
	p.genesisVerified.Store(true)
	slog.Info("btc fallback provider serves the expected network", "chain", p.live.cfg.ChainIDStr, "provider", p.label(), "network", p.live.network.name)
	return nil
}

func (p directProvider) genesisHash(ctx context.Context) (string, error) {
	if p.live.restAPI {
		body, err := p.live.esploraGet(ctx, "/block-height/0")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(body)), nil
	}
	var hash string
	if err := p.live.rpc.Call(ctx, "getblockhash", &hash, 0); err != nil {
		return "", err
	}
	return hash, nil
}
