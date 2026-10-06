package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const bitcoinRESTTimeout = 30 * time.Second

type BitcoinConfig struct {
	ChainIDStr     string
	ChainName      string
	NativeSymbol   string
	NativeDecimal  uint8
	RPCURL         string
	RPCUser        string
	RPCPass        string
	Network        string
	IsTestnet      bool
	Confirmations  uint64
	FeeRateDefault int
}

// BitcoinLive talks to a Bitcoin node or an Esplora REST indexer.
// The chain service keeps the types.Chain port this client already satisfies.
type BitcoinLive struct {
	cfg          BitcoinConfig
	rpc          *rpc.RPCClient
	restAPI      bool
	http         *httpclient.Client
	esploraRetry rateLimitRetry
	feeRates     *btcFeeRateCache
	fee          chain.FeePolicy
}

var (
	_ types.Chain             = (*BitcoinLive)(nil)
	_ chain.FeePolicyScoped   = (*BitcoinLive)(nil)
	_ chain.FeePolicyReporter = (*BitcoinLive)(nil)
)

func NewBitcoinLive(cfg BitcoinConfig) *BitcoinLive {
	isREST := bitcoinRESTEndpoint(cfg.RPCURL)
	return &BitcoinLive{
		cfg:          cfg,
		rpc:          rpc.NewRPCClient(rpc.RPCClientDeps{URL: cfg.RPCURL, User: cfg.RPCUser, Password: cfg.RPCPass}),
		restAPI:      isREST,
		http:         httpclient.NewClient(bitcoinRESTTimeout),
		esploraRetry: esploraRetry(),
		feeRates:     &btcFeeRateCache{},
	}
}

func bitcoinRESTEndpoint(endpoint string) bool {
	return strings.Contains(endpoint, "blockstream.info") || strings.Contains(endpoint, "mempool.space")
}

// Endpoint is the URL the next dial uses. Callers must not log it.
func (a *BitcoinLive) Endpoint() string {
	if a == nil || a.rpc == nil {
		return ""
	}
	return a.rpc.Endpoint()
}

// ReplaceEndpoint points later dials at endpoint and leaves fee defaults
// unchanged. An empty value does not wipe the current endpoint.
func (a *BitcoinLive) ReplaceEndpoint(endpoint string) {
	if a == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	a.cfg.RPCURL = endpoint
	a.restAPI = bitcoinRESTEndpoint(endpoint)
	if a.rpc == nil {
		a.rpc = rpc.NewRPCClient(rpc.RPCClientDeps{URL: endpoint, User: a.cfg.RPCUser, Password: a.cfg.RPCPass})
		return
	}
	a.rpc.ReplaceEndpoint(endpoint)
}

// WithFeePolicy is this adapter pricing fee rates with policy; it shares the
// clients and the network fee-rate cache.
func (a *BitcoinLive) WithFeePolicy(policy chain.FeePolicy) types.Chain {
	scoped := *a
	scoped.fee = policy
	return &scoped
}

// FeePolicy is the wallet fee policy this adapter prices with.
func (a *BitcoinLive) FeePolicy() chain.FeePolicy { return a.fee }

func (a *BitcoinLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *BitcoinLive) Name() string                  { return a.cfg.ChainName }
func (a *BitcoinLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *BitcoinLive) NativeAsset() string           { return a.cfg.NativeSymbol }

// NativeDecimals is the chain row's native_decimals for amounts leaving this adapter.
func (a *BitcoinLive) NativeDecimals() int {
	if a == nil {
		return 0
	}
	return int(a.cfg.NativeDecimal)
}

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
		Fee:      fmtUnits(fee, a.cfg.NativeDecimal),
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
		sats, err := a.btcToSats(u.Amount)
		if err != nil {
			return nil, fmt.Errorf("listunspent for %s: %w", address, err)
		}
		total.Add(total, sats)
	}
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: total, Decimals: a.cfg.NativeDecimal, Human: fmtUnits(total, a.cfg.NativeDecimal)}, nil
}

// getBalanceREST fetches UTXOs via the Blockstream/mempool.space REST API
// (GET /api/address/:address/utxo) and sums the satoshi values.
func (a *BitcoinLive) getBalanceREST(ctx context.Context, address string) (bal *types.Balance, err error) {
	defer func() { err = httpclient.RedactURL(err, a.cfg.RPCURL) }()
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/address/" + address + "/utxo"
	resp, err := a.http.Do(ctx, httpclient.Request{Method: httpclient.MethodGet, URL: url})
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, fmt.Errorf("build utxo request: %w", err)
		}
		return nil, chain.Unavailable(fmt.Errorf("fetch utxos for %s: %w", address, err))
	}
	if resp.StatusCode != httpclient.StatusOK {
		return nil, chain.FromProviderHTTP(resp.StatusCode, httpclient.RedactURLText(string(resp.Body), url))
	}

	var utxos []struct {
		Value int64 `json:"value"`
	}
	if err := json.Unmarshal(resp.Body, &utxos); err != nil {
		return nil, fmt.Errorf("parse utxo response: %w", err)
	}

	total := big.NewInt(0)
	for _, u := range utxos {
		total.Add(total, big.NewInt(u.Value))
	}
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: total, Decimals: a.cfg.NativeDecimal, Human: fmtUnits(total, a.cfg.NativeDecimal)}, nil
}

func (a *BitcoinLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	return nil, fmt.Errorf("bitcoin does not support tokens")
}

func (a *BitcoinLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	return a.buildBitcoinTransfer(ctx, req)
}

// SignTransaction is required by the chain interface. Bitcoin signing lives in
// the custody service, which asks mpc for the witness and then calls
// AssembleBitcoinP2WPKH. This method does not use the private key.
func (a *BitcoinLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, _ []byte) (*types.SignedTx, error) {
	return nil, fmt.Errorf("bitcoin transactions are signed by the custody service")
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

func (a *BitcoinLive) getLatestBlockREST(ctx context.Context) (height uint64, err error) {
	defer func() { err = httpclient.RedactURL(err, a.cfg.RPCURL) }()
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/blocks/tip/height"
	resp, err := a.http.Do(ctx, httpclient.Request{Method: httpclient.MethodGet, URL: url})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, fmt.Errorf("build block height request: %w", err)
		}
		return 0, chain.Unavailable(fmt.Errorf("fetch block height: %w", err))
	}
	if resp.StatusCode != httpclient.StatusOK {
		return 0, chain.FromProviderHTTP(resp.StatusCode, httpclient.RedactURLText(string(resp.Body), url))
	}

	if err := json.Unmarshal(resp.Body, &height); err != nil {
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
			s, err := a.btcToSats(vout.Value)
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

// btcToSats converts a bitcoind coin amount into base units using the chain row's
// native decimals.
func (a *BitcoinLive) btcToSats(btc decimal.Decimal) (*big.Int, error) {
	sats, err := amount.DecimalToBaseUnits(btc.String(), a.NativeDecimals())
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
