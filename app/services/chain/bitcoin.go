package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
)

// btcDecimals is the number of decimal places of one BTC in satoshis.
const btcDecimals = 8

const bitcoinRESTTimeout = 30 * time.Second

type BitcoinConfig struct {
	ChainIDStr     string
	ChainName      string
	NativeSymbol   string
	RPCURL         string
	RPCUser        string
	RPCPass        string
	Network        string
	IsTestnet      bool
	Confirmations  uint64
	FeeRateDefault int
}

type BitcoinLive struct {
	cfg          BitcoinConfig
	rpc          *RPCClient
	restAPI      bool
	http         *http.Client
	esploraRetry rateLimitRetry
	feeRates     *btcFeeRateCache
	fee          FeePolicy
}

func NewBitcoinLive(cfg BitcoinConfig) *BitcoinLive {
	isREST := strings.Contains(cfg.RPCURL, "blockstream.info") ||
		strings.Contains(cfg.RPCURL, "mempool.space")
	return &BitcoinLive{
		cfg:          cfg,
		rpc:          NewRPCClient(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass),
		restAPI:      isREST,
		http:         httpclient.New(bitcoinRESTTimeout),
		esploraRetry: esploraRetry(),
		feeRates:     &btcFeeRateCache{},
	}
}

// WithFeePolicy is this adapter pricing fee rates with policy; it shares the
// clients and the network fee-rate cache.
func (a *BitcoinLive) WithFeePolicy(policy FeePolicy) types.Chain {
	scoped := *a
	scoped.fee = policy
	return &scoped
}

// FeePolicy is the wallet fee policy this adapter prices with.
func (a *BitcoinLive) FeePolicy() FeePolicy { return a.fee }

func (a *BitcoinLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *BitcoinLive) Name() string                  { return a.cfg.ChainName }
func (a *BitcoinLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *BitcoinLive) NativeAsset() string           { return a.cfg.NativeSymbol }

// IsTestnet reports whether the chain record points at Bitcoin testnet (tb1 addresses).
func (a *BitcoinLive) IsTestnet() bool { return a.cfg.IsTestnet }

func (a *BitcoinLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	return "", fmt.Errorf("BTC key derivation not implemented — use BIP-84 + hdkeychain")
}

func (a *BitcoinLive) ValidateAddress(address string) bool {
	if len(address) < 26 || len(address) > 62 {
		return false
	}
	if a.cfg.IsTestnet {
		return (len(address) >= 3 && address[:3] == "tb1") || address[0] == 'm' || address[0] == 'n' || address[0] == '2'
	}
	return address[:3] == "bc1" || address[0] == '1' || address[0] == '3'
}

// EstimateFee prices a transfer with the same fee policy and coin selection as
// BuildTransfer: the exact fee when From's confirmed UTXOs cover Amount, otherwise
// a typical one-input, payment-plus-change transaction at the current rate.
func (a *BitcoinLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	fee := big.NewInt(a.estimateTransferFeeSats(ctx, req))

	symbol := "BTC"
	if a.cfg.IsTestnet {
		symbol = "TBTC"
	}

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, 8),
		FeeAsset: symbol,
	}, nil
}

func (a *BitcoinLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	if a.restAPI {
		return a.getBalanceREST(ctx, address)
	}
	return a.getBalanceRPC(ctx, address)
}

func (a *BitcoinLive) getBalanceRPC(ctx context.Context, address string) (*types.Balance, error) {
	var utxos []struct {
		Amount decimal.Decimal `json:"amount"`
	}
	if err := a.rpc.Call(ctx, "listunspent", &utxos, 0, 9999999, []string{address}); err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, u := range utxos {
		sats, err := btcToSats(u.Amount)
		if err != nil {
			return nil, fmt.Errorf("listunspent for %s: %w", address, err)
		}
		total.Add(total, sats)
	}
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: total, Decimals: 8, Human: fmtUnits(total, 8)}, nil
}

// getBalanceREST fetches UTXOs via the Blockstream/mempool.space REST API
// (GET /api/address/:address/utxo) and sums the satoshi values.
func (a *BitcoinLive) getBalanceREST(ctx context.Context, address string) (*types.Balance, error) {
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/address/" + address + "/utxo"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build utxo request: %w", err)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch utxos for %s: %w", address, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read utxo response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("utxo API returned %d: %s", resp.StatusCode, string(body))
	}

	var utxos []struct {
		Value int64 `json:"value"`
	}
	if err := json.Unmarshal(body, &utxos); err != nil {
		return nil, fmt.Errorf("parse utxo response: %w", err)
	}

	total := big.NewInt(0)
	for _, u := range utxos {
		total.Add(total, big.NewInt(u.Value))
	}
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: total, Decimals: 8, Human: fmtUnits(total, 8)}, nil
}

func (a *BitcoinLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	return nil, fmt.Errorf("bitcoin does not support tokens")
}

func (a *BitcoinLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	return a.buildBitcoinTransfer(ctx, req)
}

func (a *BitcoinLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	return signBitcoinP2WPKH(unsigned, privateKey, netParams(unsigned))
}

func (a *BitcoinLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return a.broadcastBitcoin(ctx, signed)
}

func (a *BitcoinLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	if a.restAPI {
		return a.getLatestBlockREST(ctx)
	}
	var count uint64
	if err := a.rpc.Call(ctx, "getblockcount", &count); err != nil {
		return 0, err
	}
	return count, nil
}

func (a *BitcoinLive) getLatestBlockREST(ctx context.Context) (uint64, error) {
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/blocks/tip/height"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("build block height request: %w", err)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch block height: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read block height response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("block height API returned %d: %s", resp.StatusCode, string(body))
	}

	var height uint64
	if err := json.Unmarshal(body, &height); err != nil {
		return 0, fmt.Errorf("parse block height: %w", err)
	}
	return height, nil
}

func (a *BitcoinLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	if a.restAPI {
		return a.scanBlockREST(ctx, blockNum)
	}
	return a.scanBlockRPC(ctx, blockNum)
}

func (a *BitcoinLive) scanBlockRPC(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	var hash string
	if err := a.rpc.Call(ctx, "getblockhash", &hash, blockNum); err != nil {
		return nil, err
	}
	var block struct {
		Time int64 `json:"time"`
		Tx   []struct {
			Txid string `json:"txid"`
			Vout []struct {
				Value        decimal.Decimal `json:"value"`
				ScriptPubKey struct {
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				} `json:"scriptPubKey"`
			} `json:"vout"`
		} `json:"tx"`
	}
	if err := a.rpc.Call(ctx, "getblock", &block, hash, 2); err != nil {
		return nil, err
	}

	blockTime := time.Unix(block.Time, 0)
	var transfers []types.DetectedTransfer
	for _, tx := range block.Tx {
		for _, vout := range tx.Vout {
			if !vout.Value.IsPositive() {
				continue
			}
			addr := vout.ScriptPubKey.Address
			if addr == "" && len(vout.ScriptPubKey.Addresses) > 0 {
				addr = vout.ScriptPubKey.Addresses[0]
			}
			if addr == "" {
				continue
			}
			s, err := btcToSats(vout.Value)
			if err != nil {
				return nil, fmt.Errorf("block %d tx %s: %w", blockNum, tx.Txid, err)
			}
			transfers = append(transfers, types.DetectedTransfer{
				TxHash: tx.Txid, BlockNumber: blockNum, BlockHash: hash,
				To: addr, Amount: s, Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
			})
		}
	}
	return transfers, nil
}

// btcToSats converts a bitcoind BTC amount (JSON number with up to 8 decimals) to
// satoshis exactly.
func btcToSats(btc decimal.Decimal) (*big.Int, error) {
	sats, err := numeric.ToBaseUnits(btc, btcDecimals)
	if err != nil {
		return nil, fmt.Errorf("btc amount %s: %w", btc.String(), err)
	}
	return sats, nil
}

func (a *BitcoinLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	return a.buildBitcoinSweep(ctx, req)
}

// GetTransactionBlock returns the height of the block that confirmed txHash, or 0
// while it is unconfirmed (or not yet known to the node/indexer), which the
// confirmation loop treats as still pending. Errors are failed lookups.
func (a *BitcoinLive) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	if a.restAPI {
		return a.getTransactionBlockREST(ctx, txHash)
	}
	return a.getTransactionBlockRPC(ctx, txHash)
}

func (a *BitcoinLive) GasReadinessThreshold() *big.Int { return nil }

func (a *BitcoinLive) DustThreshold(asset string) *big.Int { return nil }

// EstimateGasPrice returns nil for Bitcoin: fees are per-vByte on the
// UTXO itself, not a gas-price × gas-limit product, so there is no scalar
// that composes with the planner's EVM gas-limit model.
func (a *BitcoinLive) EstimateGasPrice(ctx context.Context) (*big.Int, error) { return nil, nil }
