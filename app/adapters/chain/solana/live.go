package solana

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/pkg/types"
)

type SolanaConfig struct {
	ChainIDStr    string
	ChainName     string
	NativeSymbol  string
	RPCURL        string
	Confirmations uint64
}

// SolanaLive talks to a Solana JSON-RPC node.
// The chain service keeps the types.Chain port this client already satisfies.
type SolanaLive struct {
	cfg SolanaConfig
	rpc *rpc.RPCClient
}

var _ types.Chain = (*SolanaLive)(nil)

func NewSolanaLive(cfg SolanaConfig) *SolanaLive {
	return &SolanaLive{cfg: cfg, rpc: rpc.NewRPCClient(rpc.RPCClientDeps{URL: cfg.RPCURL})}
}

// Endpoint is the URL the next dial uses. Callers must not log it.
func (a *SolanaLive) Endpoint() string {
	if a == nil || a.rpc == nil {
		return ""
	}
	return a.rpc.Endpoint()
}

// ReplaceEndpoint points later dials at endpoint. An empty value does not
// wipe the current endpoint.
func (a *SolanaLive) ReplaceEndpoint(endpoint string) {
	if a == nil {
		return
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return
	}
	a.cfg.RPCURL = endpoint
	if a.rpc == nil {
		a.rpc = rpc.NewRPCClient(rpc.RPCClientDeps{URL: endpoint})
		return
	}
	a.rpc.ReplaceEndpoint(endpoint)
}

func (a *SolanaLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *SolanaLive) Name() string                  { return a.cfg.ChainName }
func (a *SolanaLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *SolanaLive) NativeAsset() string           { return a.cfg.NativeSymbol }

func (a *SolanaLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	return "", fmt.Errorf("SOL key derivation not implemented — use ed25519 SLIP-0010")
}

func (a *SolanaLive) ValidateAddress(address string) bool {
	if len(address) < 32 || len(address) > 44 {
		return false
	}
	const base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, c := range address {
		found := false
		for _, b := range base58 {
			if c == b {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (a *SolanaLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	fee := new(big.Int).SetInt64(5000)

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, 9),
		FeeAsset: a.cfg.NativeSymbol,
	}, nil
}

// NativeTransferReserve returns what a native transfer costs its source besides the
// amount (one signature fee) and the rent-exempt minimum a system account must keep
// unless it is emptied completely.
func (a *SolanaLive) NativeTransferReserve(ctx context.Context) (fee, minimumRemaining *big.Int, err error) {
	rentExemptLamports, err := a.rentExemptMinimum(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	return big.NewInt(solanaNativeFeeLamports), rentExemptLamports, nil
}

func (a *SolanaLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	var result struct {
		Value uint64 `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getBalance", &result, address, map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return nil, err
	}
	bal := new(big.Int).SetUint64(result.Value)
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: bal, Decimals: 9, Human: fmtUnits(bal, 9)}, nil
}

func (a *SolanaLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	return a.buildSolanaTransfer(ctx, req)
}

// SignTransaction is required by the chain interface. Solana signing lives in
// the custody service: a child seed goes through mpc.SignEd25519Seed and a
// genesis scalar through chain.SignEd25519WithScalar, then AssembleSolana.
// This method does not use the key.
func (a *SolanaLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, _ []byte) (*types.SignedTx, error) {
	return nil, fmt.Errorf("solana transactions are signed by the custody service")
}

func (a *SolanaLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return broadcastSolanaTx(ctx, a, signed)
}

func (a *SolanaLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	var slot uint64
	if err := a.rpc.Call(ctx, "getSlot", &slot, map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return 0, err
	}
	return slot, nil
}

func (a *SolanaLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	return a.buildSolanaSweep(ctx, req)
}

func (a *SolanaLive) GasReadinessThreshold() *big.Int { return nil }

func (a *SolanaLive) DustThreshold(asset string) *big.Int { return nil }

// EstimateGasPrice returns nil for Solana: fees are flat (5_000 lamports per
// signature) and priority fees are handled through a separate compute-unit
// model rather than a universal gas price, so the sweep planner's gas
// estimation is not meaningful here.
func (a *SolanaLive) EstimateGasPrice(ctx context.Context) (*big.Int, error) { return nil, nil }
