// Package xrp is the XRP Ledger adapter. Classic r-addresses come from
// addressing.DeriveXRPAddress. It reads a balance and the validated ledger,
// scans validated ledgers for native XRP deposits (ScanBlock), and it can build,
// sign, and broadcast one native Payment on the altnet (network id 1). Sweeps and
// issued currencies are not implemented.
package xrp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// DropsPerXRP is the base unit: 1 XRP = 1_000_000 drops.
	DropsPerXRP = 1_000_000
	// NativeDecimals is the drop scale.
	NativeDecimals = 6

	xrpRPCTimeout = 15 * time.Second
	// xrpRPCMaxResponseBytes is the most one rippled answer may be. Past this
	// the read fails instead of truncating.
	xrpRPCMaxResponseBytes = 1 << 20
)

// ErrPaymentsNotImplemented is returned by BuildSweep. A native Payment is
// built, signed, and broadcast by BuildTransfer, SignTransaction, and
// BroadcastTransaction. Nothing else is sent.
var ErrPaymentsNotImplemented = errors.New("xrp sweeps are not implemented")

// Config is everything that differs between XRP Ledger networks. The testnet
// record points at the public altnet, not mainnet.
type Config struct {
	ChainIDStr   string
	ChainName    string
	NativeSymbol string
	RPCURL       string
	// RPCFallbacks are tried in order after RPCURL when it cannot serve a call
	// (rpc.Endpoints failover). Each answers server_info's network_id, which must
	// be NetworkID (decimal), before its first use; an empty NetworkID checks none.
	RPCFallbacks []chain.RPCFallback
	NetworkID    string
	// RPCHealth receives the health of the endpoints each time it changes; nil
	// records nothing.
	RPCHealth             chain.RPCHealthSink
	IsTestnet             bool
	Confirmations         uint64
	GasReadinessThreshold *big.Int
	DustThresholdNative   *big.Int
}

// Live is the XRP Ledger adapter. Payments are limited to the altnet testnet.
type Live struct {
	cfg       Config
	client    *httpclient.Client
	endpoints *rpc.Endpoints
}

var _ types.Chain = (*Live)(nil)

// NewLive builds an adapter. It does not dial the server.
func NewLive(cfg Config) *Live {
	adapter := &Live{cfg: cfg, client: httpclient.NewClient(xrpRPCTimeout)}
	adapter.endpoints = rpc.NewEndpoints(rpc.EndpointsDeps{
		URL:       cfg.RPCURL,
		Fallbacks: cfg.RPCFallbacks,
		ChainID:   cfg.ChainIDStr,
		Identity:  adapter.networkIDOf,
		Network:   strings.TrimSpace(cfg.NetworkID),
		Health:    cfg.RPCHealth,
	})
	return adapter
}

// ReplaceEndpoint points later calls at endpoint as the primary and keeps the
// fallbacks. An empty endpoint is ignored. The endpoint is not logged.
func (a *Live) ReplaceEndpoint(endpoint string) {
	if a == nil {
		return
	}
	a.endpoints.ReplaceEndpoints(endpoint, a.endpoints.Fallbacks())
}

// ReplaceEndpoints points later calls at primary and then fallbacks, in order. An
// empty primary is ignored.
func (a *Live) ReplaceEndpoints(primary string, fallbacks []chain.RPCFallback) {
	if a == nil {
		return
	}
	a.endpoints.ReplaceEndpoints(primary, fallbacks)
}

// Endpoint is the URL of the primary endpoint. Callers must not log it.
func (a *Live) Endpoint() string {
	if a == nil {
		return ""
	}
	return a.endpoints.Endpoint()
}

// Mainnet's first full-history ledger and its hash (read on 2026-10-20 from
// xrplcluster.com and s1.ripple.com): a server that holds it is on mainnet.
const (
	xrpMainnetFirstLedger     = 32570
	xrpMainnetFirstLedgerHash = "4109C6F2045FC7EFF4CDE8F9905D19C28820D86304080FF886B299F0206E42B5"
)

// NetworkID is the network_id (decimal) the rippled server rawURL reports in
// server_info: it names the network (models.XRPLNetworkID). A server that does not
// report one is on mainnet (0) only when it holds mainnet's first full-history
// ledger (validated, with its hash); any other is an error, never taken for mainnet.
func NetworkID(ctx context.Context, rawURL string) (string, error) {
	return NewLive(Config{RPCURL: rawURL}).networkIDOf(ctx, rawURL)
}

// networkIDOf asks rawURL alone for server_info's network_id.
func (a *Live) networkIDOf(ctx context.Context, rawURL string) (string, error) {
	body, err := json.Marshal(xrpRPCRequest{Method: "server_info", Params: []any{map[string]any{}}})
	if err != nil {
		return "", err
	}
	result, _, err := a.exchange(ctx, rawURL, "server_info", body, xrpRPCMaxResponseBytes)
	if err != nil {
		return "", httpclient.RedactURL(err, rawURL)
	}
	var info struct {
		xrpRPCError
		Info struct {
			NetworkID json.Number `json:"network_id"`
		} `json:"info"`
	}
	if decodeErr := decodeXRPResult("server_info", result, &info); decodeErr != nil {
		return "", decodeErr
	}
	if info.Status != "success" {
		return "", fmt.Errorf("xrp server_info: %s", xrpErrorCode(info.xrpRPCError))
	}
	if info.Info.NetworkID == "" {
		return a.mainnetByHistory(ctx, rawURL)
	}
	id, err := nonNegativeJSON(info.Info.NetworkID)
	if err != nil {
		return "", fmt.Errorf("xrp server_info network_id: %w", err)
	}
	return strconv.FormatUint(id, 10), nil
}

// mainnetByHistory is "0" when rawURL holds mainnet's first full-history ledger,
// validated with its hash; else an error: the server's network is not known.
func (a *Live) mainnetByHistory(ctx context.Context, rawURL string) (string, error) {
	body, err := json.Marshal(xrpRPCRequest{Method: "ledger", Params: []any{map[string]any{
		"ledger_index": xrpMainnetFirstLedger, "transactions": false, "expand": false,
	}}})
	if err != nil {
		return "", err
	}
	result, _, err := a.exchange(ctx, rawURL, "ledger", body, xrpRPCMaxResponseBytes)
	if err != nil {
		return "", fmt.Errorf("xrp server_info reports no network_id and mainnet's history cannot be read: %w", httpclient.RedactURL(err, rawURL))
	}
	var ledger struct {
		xrpRPCError
		Validated  bool   `json:"validated"`
		LedgerHash string `json:"ledger_hash"`
		Ledger     struct {
			LedgerHash string `json:"ledger_hash"`
		} `json:"ledger"`
	}
	if decodeErr := decodeXRPResult("ledger", result, &ledger); decodeErr != nil {
		return "", decodeErr
	}
	hash := ledger.LedgerHash
	if hash == "" {
		hash = ledger.Ledger.LedgerHash
	}
	if ledger.Status != "success" || !ledger.Validated || !strings.EqualFold(hash, xrpMainnetFirstLedgerHash) {
		return "", fmt.Errorf("xrp server_info reports no network_id and the server does not hold mainnet's ledger %d", xrpMainnetFirstLedger)
	}
	return models.XRPLNetworkID(models.NetworkXRPLMainnet), nil
}

func (a *Live) ID() string                    { return a.cfg.ChainIDStr }
func (a *Live) Name() string                  { return a.cfg.ChainName }
func (a *Live) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *Live) NativeAsset() string           { return a.cfg.NativeSymbol }
func (a *Live) IsTestnet() bool               { return a.cfg.IsTestnet }

// DeriveAddress is unused: deposit addresses come from addressing.DeriveXRPAddress.
func (a *Live) DeriveAddress(_ []byte, _ uint32) (string, error) {
	return "", fmt.Errorf("XRP key derivation is not done here — addresses come from addressing.DeriveXRPAddress")
}

func (a *Live) ValidateAddress(address string) bool {
	return addressing.IsXRPClassicAddress(address)
}

func (a *Live) GasReadinessThreshold() *big.Int { return a.cfg.GasReadinessThreshold }

// DustThreshold returns the native drop threshold. Other assets have none:
// this experiment lists no issued currencies.
func (a *Live) DustThreshold(asset string) *big.Int {
	if types.SameAssetSymbol(asset, a.cfg.NativeSymbol) {
		return a.cfg.DustThresholdNative
	}
	return nil
}

// GetBalance reads account_info on the validated ledger. A missing account
// (never funded) is a zero balance. The amount is an integer number of drops.
func (a *Live) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	if !addressing.IsXRPClassicAddress(address) {
		return nil, fmt.Errorf("xrp balance: %q is not a classic address", address)
	}
	drops, err := a.accountDrops(ctx, address)
	if err != nil {
		return nil, err
	}
	return &types.Balance{
		Address:  address,
		Asset:    a.cfg.NativeSymbol,
		Amount:   drops,
		Decimals: NativeDecimals,
		Human:    amount.FormatBaseUnits(drops, NativeDecimals),
	}, nil
}

// GetTokenBalance refuses: this experiment has no issued currencies.
func (a *Live) GetTokenBalance(context.Context, string, types.Token) (*types.Balance, error) {
	return nil, fmt.Errorf("xrp: no issued currencies in this experiment")
}

func (a *Live) BuildSweep(context.Context, types.SweepRequest) ([]types.UnsignedTx, error) {
	return nil, ErrPaymentsNotImplemented
}

// GetLatestBlock is the validated ledger index.
func (a *Live) GetLatestBlock(ctx context.Context) (uint64, error) {
	return a.validatedLedgerIndex(ctx)
}

// GetTransactionBlock is the validated ledger of txHash, or 0 when the
// payment is not in a validated ledger yet.
func (a *Live) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	return a.transactionLedger(ctx, txHash)
}

// EstimateGasPrice does not apply: an XRP fee is a drop bid, not a gas price.
func (a *Live) EstimateGasPrice(context.Context) (*big.Int, error) {
	return nil, nil
}
