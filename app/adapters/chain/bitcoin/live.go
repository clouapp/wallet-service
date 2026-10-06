package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// bitcoinRPCMaxResponseBytes bounds one JSON-RPC answer: getblock at verbosity 2
// is ~7 times the block size (a 60 kB Litecoin block is 400 kB of JSON), so a full
// block needs far more than the 1 MiB default.
const bitcoinRPCMaxResponseBytes = esploraMaxResponseBytes

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
	// Fallbacks are tried in order when the provider at RPCURL fails (see
	// bitcoinFailover); empty keeps RPCURL as the only provider.
	Fallbacks []BitcoinFallback
	// TatumDataAPIURL is the Tatum Data API base a keyed Tatum fallback reads UTXOs
	// from; empty means DefaultTatumDataAPIURL.
	TatumDataAPIURL string
}

// BitcoinFallback is a secondary provider: an Esplora or bitcoind JSON-RPC URL, an
// Electrum server (electrum+ssl://host:port?cert_sha256=HEX) or a Tatum gateway
// (https://*.tatum.io). APIKey, optional, is the Tatum key: it is sent as x-api-key
// to Tatum hosts only, never to another provider.
type BitcoinFallback struct {
	URL    string
	APIKey string
}

// BitcoinLive talks to a Bitcoin-family node or an Esplora REST indexer.
// Litecoin shares the transaction format and differs by network parameters.
// The chain service keeps the types.Chain port this client already satisfies.
type BitcoinLive struct {
	cfg          BitcoinConfig
	network      bitcoinNetwork
	rpc          *rpc.RPCClient
	restAPI      bool
	http         *httpclient.Client
	esploraRetry rateLimitRetry
	feeRates     *btcFeeRateCache
	// feeRateTimeout bounds one fee-rate fetch; zero means btcFeeRateFetchTimeout.
	feeRateTimeout time.Duration
	fee            chain.FeePolicy
	// apiKey, when set, goes out as the x-api-key header (Tatum fallbacks only).
	apiKey    string
	providers *bitcoinFailover
}

var (
	_ types.Chain             = (*BitcoinLive)(nil)
	_ chain.FeePolicyScoped   = (*BitcoinLive)(nil)
	_ chain.FeePolicyReporter = (*BitcoinLive)(nil)
)

func (a *BitcoinLive) feeAsset() string {
	if a != nil && a.network.feeAsset != "" {
		return a.network.feeAsset
	}
	if a == nil {
		return ""
	}
	return a.cfg.NativeSymbol
}

func NewBitcoinLive(cfg BitcoinConfig) *BitcoinLive {
	live := &BitcoinLive{
		cfg:          cfg,
		network:      bitcoinNetworkOf(cfg),
		rpc:          rpc.NewRPCClient(rpc.RPCClientDeps{URL: cfg.RPCURL, User: cfg.RPCUser, Password: cfg.RPCPass}).WithMaxResponseBytes(bitcoinRPCMaxResponseBytes),
		restAPI:      isEsploraURL(cfg.RPCURL),
		http:         httpclient.NewClient(bitcoinRESTTimeout),
		esploraRetry: esploraRetry(),
		feeRates:     &btcFeeRateCache{},
	}
	providers := []bitcoinProvider{newDirectProvider(live, btcPrimaryProviderName, false)}
	for i, fallback := range cfg.Fallbacks {
		provider, err := live.fallbackProvider(i+1, fallback)
		if err != nil {
			slog.Warn("btc fallback provider ignored", "chain", cfg.ChainIDStr, "fallback", i+1, "error", err)
			continue
		}
		providers = append(providers, provider)
	}
	live.providers = newBitcoinFailover(cfg.ChainIDStr, providers...)
	return live
}

const btcPrimaryProviderName = "primary"

// fallbackProvider builds the n-th fallback: an Electrum server, or a copy of this
// adapter pointed at another Esplora/JSON-RPC URL that must prove its network first.
func (a *BitcoinLive) fallbackProvider(n int, fallback BitcoinFallback) (bitcoinProvider, error) {
	rawURL := strings.TrimSpace(fallback.URL)
	name := fmt.Sprintf("fallback-%d", n)
	if rawURL == "" {
		return nil, fmt.Errorf("empty URL")
	}
	if isElectrumURL(rawURL) {
		return newElectrumProvider(name, rawURL, a.network, a.cfg.NativeSymbol)
	}
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil, fmt.Errorf("unsupported scheme: want https://, http:// or %s", electrumSSLScheme)
	}
	tatum := isTatumURL(rawURL)
	apiKey := ""
	if tatum {
		apiKey = fallback.APIKey
	}
	direct := newDirectProvider(a.secondary(rawURL, apiKey), name, true)
	if !tatum {
		return direct, nil
	}
	data, err := newTatumDataAPI(a.cfg.TatumDataAPIURL, apiKey, a.network.name, httpclient.New(bitcoinRESTTimeout))
	if err != nil {
		return nil, err
	}
	return newTatumProvider(direct, data), nil
}

// secondary is a copy of this adapter pointed at rawURL; apiKey, when set, goes out
// as the x-api-key header.
func (a *BitcoinLive) secondary(rawURL, apiKey string) *BitcoinLive {
	copyCfg := a.cfg
	copyCfg.RPCURL = rawURL
	copyCfg.RPCUser, copyCfg.RPCPass = "", ""
	copyCfg.Fallbacks = nil
	return &BitcoinLive{
		cfg:          copyCfg,
		network:      a.network,
		rpc:          rpc.NewRPCClient(rpc.RPCClientDeps{URL: rawURL}).WithHeader(apiKeyHeader, apiKey).WithMaxResponseBytes(bitcoinRPCMaxResponseBytes),
		restAPI:      isEsploraURL(rawURL),
		http:         a.http,
		esploraRetry: esploraRetry(),
		feeRates:     a.feeRates,
		apiKey:       apiKey,
	}
}

// chainProviders is the failover of this adapter; a zero-value adapter talks to
// RPCURL only.
func (a *BitcoinLive) chainProviders() *bitcoinFailover {
	if a.providers == nil {
		return newBitcoinFailover(a.cfg.ChainIDStr, newDirectProvider(a, btcPrimaryProviderName, false))
	}
	return a.providers
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

// IsTestnet reports whether the chain record points at a test network (tb1 / tltc1
// addresses).
func (a *BitcoinLive) IsTestnet() bool {
	if a == nil {
		return false
	}
	return a.network.testnet
}

func (a *BitcoinLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	return "", fmt.Errorf("BTC key derivation not implemented — use BIP-84 + hdkeychain")
}

// ValidateAddress accepts exactly what the builder can pay: a P2WPKH address of this
// adapter's network. Legacy, P2SH, P2WSH and taproot destinations are refused here
// rather than failing later, when the withdrawal is signed.
func (a *BitcoinLive) ValidateAddress(address string) bool {
	_, err := witnessProgram(address, a.network.params)
	return err == nil
}

// EstimateFee prices a transfer with the same fee policy and coin selection as
// BuildTransfer: the exact fee when From's confirmed UTXOs cover Amount, otherwise
// a typical one-input, payment-plus-change transaction at the current rate.
func (a *BitcoinLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	fee := big.NewInt(a.estimateTransferFeeSats(ctx, req))
	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, a.cfg.NativeDecimal),
		FeeAsset: a.feeAsset(),
	}, nil
}

func (a *BitcoinLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	return a.chainProviders().balance(ctx, address)
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

// getBalanceREST sums the UTXOs of GET /address/:address/utxo (Esplora), confirmed
// or not.
func (a *BitcoinLive) getBalanceREST(ctx context.Context, address string) (*types.Balance, error) {
	if strings.TrimSpace(address) == "" || strings.ContainsAny(address, "/?#") {
		return nil, fmt.Errorf("btc balance: invalid address %q", address)
	}
	body, err := a.esploraGet(ctx, "/address/"+address+"/utxo")
	if err != nil {
		return nil, fmt.Errorf("fetch utxos for %s: %w", address, err)
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
	if err := a.requireBuiltOnThisNetwork(unsigned); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("bitcoin transactions are signed by the custody service")
}

func (a *BitcoinLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return a.broadcastBitcoin(ctx, signed)
}

func (a *BitcoinLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	return a.chainProviders().latestBlock(ctx)
}

func (a *BitcoinLive) getLatestBlockREST(ctx context.Context) (uint64, error) {
	body, err := a.esploraGet(ctx, "/blocks/tip/height")
	if err != nil {
		return 0, fmt.Errorf("fetch block height: %w", err)
	}
	var height uint64
	if err := json.Unmarshal(body, &height); err != nil {
		return 0, fmt.Errorf("parse block height: %w", err)
	}
	return height, nil
}

func (a *BitcoinLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	return a.chainProviders().scanBlock(ctx, blockNum)
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
	txID, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return 0, err
	}
	return a.chainProviders().transactionBlock(ctx, txID)
}

func (a *BitcoinLive) GasReadinessThreshold() *big.Int { return nil }

func (a *BitcoinLive) DustThreshold(asset string) *big.Int { return nil }

// EstimateGasPrice returns nil for Bitcoin: fees are per-vByte on the
// UTXO itself, not a gas-price × gas-limit product, so there is no scalar
// that composes with the planner's EVM gas-limit model.
func (a *BitcoinLive) EstimateGasPrice(ctx context.Context) (*big.Int, error) { return nil, nil }
