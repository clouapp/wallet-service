// Package xrp is the XRP Ledger adapter. Classic r-addresses come from
// addressing.DeriveXRPAddress. This experiment reads a balance and the
// validated ledger index. It does not build, sign, or broadcast a payment.
package xrp

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/addressing"
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

// ErrPaymentsNotImplemented is returned by every method that would build, sign,
// or broadcast a payment. Nothing is signed and nothing is sent.
var ErrPaymentsNotImplemented = errors.New("xrp payments are not implemented: this experiment derives a classic address and reads the ledger")

// Config is everything that differs between XRP Ledger networks. The testnet
// record points at the public altnet, not mainnet.
type Config struct {
	ChainIDStr            string
	ChainName             string
	NativeSymbol          string
	RPCURL                string
	IsTestnet             bool
	Confirmations         uint64
	GasReadinessThreshold *big.Int
	DustThresholdNative   *big.Int
}

// Live is the read-only XRP Ledger adapter.
type Live struct {
	cfg  Config
	http *http.Client

	mu     sync.Mutex
	rpcURL string
}

var _ types.Chain = (*Live)(nil)

// NewLive builds an adapter. It does not dial the server.
func NewLive(cfg Config) *Live {
	return &Live{
		cfg:    cfg,
		http:   httpclient.New(xrpRPCTimeout),
		rpcURL: cfg.RPCURL,
	}
}

// ReplaceEndpoint points the next read at endpoint. An empty endpoint is ignored.
// The endpoint is not logged.
func (a *Live) ReplaceEndpoint(endpoint string) {
	if a == nil || endpoint == "" {
		return
	}
	a.mu.Lock()
	a.rpcURL = endpoint
	a.mu.Unlock()
}

func (a *Live) endpoint() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rpcURL
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

func (a *Live) BuildTransfer(context.Context, types.TransferRequest) (*types.UnsignedTx, error) {
	return nil, ErrPaymentsNotImplemented
}

func (a *Live) SignTransaction(context.Context, *types.UnsignedTx, []byte) (*types.SignedTx, error) {
	return nil, ErrPaymentsNotImplemented
}

func (a *Live) BroadcastTransaction(context.Context, *types.SignedTx) (string, error) {
	return "", ErrPaymentsNotImplemented
}

func (a *Live) BuildSweep(context.Context, types.SweepRequest) ([]types.UnsignedTx, error) {
	return nil, ErrPaymentsNotImplemented
}

func (a *Live) EstimateFee(context.Context, types.TransferRequest) (*types.FeeEstimate, error) {
	return nil, ErrPaymentsNotImplemented
}

// GetLatestBlock is the validated ledger index.
func (a *Live) GetLatestBlock(ctx context.Context) (uint64, error) {
	return a.validatedLedgerIndex(ctx)
}

// ScanBlock is not implemented. Returning an error keeps a scanner from
// treating a ledger as empty.
func (a *Live) ScanBlock(context.Context, uint64) ([]types.DetectedTransfer, error) {
	return nil, fmt.Errorf("xrp ledger scan is not implemented in this experiment")
}

// GetTransactionBlock is unused: outbound payments are not built here.
func (a *Live) GetTransactionBlock(context.Context, string) (uint64, error) {
	return 0, nil
}

// EstimateGasPrice does not apply: an XRP fee is a drop bid, not a gas price.
func (a *Live) EstimateGasPrice(context.Context) (*big.Int, error) {
	return nil, nil
}
