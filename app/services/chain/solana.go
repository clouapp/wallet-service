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

func (a *SolanaLive) ID() string                        { return a.cfg.ChainIDStr }
func (a *SolanaLive) Name() string                      { return a.cfg.ChainName }
func (a *SolanaLive) RequiredConfirmations() uint64      { return a.cfg.Confirmations }
func (a *SolanaLive) NativeAsset() string                { return a.cfg.NativeSymbol }

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

func (a *SolanaLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	// Need ATA derivation — stub for POC
	return nil, fmt.Errorf("SOL token balance not implemented — need ATA derivation")
}

func (a *SolanaLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	var blockhash string
	var result struct {
		Value struct {
			Blockhash string `json:"blockhash"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getLatestBlockhash", &result, map[string]string{"commitment": "finalized"}); err != nil {
		return nil, err
	}
	blockhash = result.Value.Blockhash

	return &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"from": req.From, "to": req.To, "amount": req.Amount.String(),
			"blockhash": blockhash, "is_spl": req.Token != nil,
		},
	}, nil
}

func (a *SolanaLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	return nil, fmt.Errorf("SOL signing not implemented — use ed25519")
}

func (a *SolanaLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	return "", fmt.Errorf("SOL broadcast not implemented")
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
