package chain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

type SolanaConfig struct {
	ChainIDStr    string
	ChainName     string
	NativeSymbol  string
	RPCURL        string
	Confirmations uint64
}

type SolanaLive struct {
	cfg SolanaConfig
	rpc *RPCClient
}

func NewSolanaLive(cfg SolanaConfig) *SolanaLive {
	return &SolanaLive{cfg: cfg, rpc: NewRPCClient(cfg.RPCURL, "", "")}
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

func (a *SolanaLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	var result struct {
		Value uint64 `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getBalance", &result, address, map[string]string{"commitment": "finalized"}); err != nil {
		return nil, err
	}
	bal := new(big.Int).SetUint64(result.Value)
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: bal, Decimals: 9, Human: fmtUnits(bal, 9)}, nil
}

func (a *SolanaLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	return a.buildSolanaTransfer(ctx, req)
}

func (a *SolanaLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	return signSolanaTx(unsigned, privateKey)
}

func (a *SolanaLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return broadcastSolanaTx(ctx, a, signed)
}

// GetTransactionBlock is a no-op for Solana in v1: the outbound confirmation
// reconciliation loop (sweep/withdrawal/gas_seed) is EVM-only in this release,
// so we return (0, nil) to signal "treat as still pending" without breaking
// the interface.
func (a *SolanaLive) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	return 0, nil
}

func (a *SolanaLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	var slot uint64
	if err := a.rpc.Call(ctx, "getSlot", &slot, map[string]string{"commitment": "finalized"}); err != nil {
		return 0, err
	}
	return slot, nil
}

func (a *SolanaLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	var block struct {
		BlockTime    int64 `json:"blockTime"`
		Transactions []struct {
			Transaction struct {
				Signatures []string `json:"signatures"`
			} `json:"transaction"`
			Meta *struct {
				Err          interface{} `json:"err"`
				PreBalances  []uint64    `json:"preBalances"`
				PostBalances []uint64    `json:"postBalances"`
			} `json:"meta"`
		} `json:"transactions"`
	}

	if err := a.rpc.Call(ctx, "getBlock", &block, blockNum, map[string]interface{}{
		"encoding": "jsonParsed", "transactionDetails": "full",
		"commitment": "finalized", "maxSupportedTransactionVersion": 0,
	}); err != nil {
		return nil, err
	}

	blockTime := time.Unix(block.BlockTime, 0)
	var transfers []types.DetectedTransfer

	for _, txWrap := range block.Transactions {
		if txWrap.Meta == nil || txWrap.Meta.Err != nil {
			continue
		}
		// SOL native: diff pre/post balances
		for i := range txWrap.Meta.PreBalances {
			if i >= len(txWrap.Meta.PostBalances) {
				break
			}
			pre, post := txWrap.Meta.PreBalances[i], txWrap.Meta.PostBalances[i]
			if post > pre {
				transfers = append(transfers, types.DetectedTransfer{
					TxHash: txWrap.Transaction.Signatures[0], BlockNumber: blockNum,
					Amount: new(big.Int).SetUint64(post - pre), Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
				})
			}
		}
	}

	return transfers, nil
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
