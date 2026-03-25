package chain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

type BitcoinConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string
	Network string
}

type BitcoinLive struct {
	cfg BitcoinConfig
	rpc *RPCClient
}

func NewBitcoinLive(cfg BitcoinConfig) *BitcoinLive {
	return &BitcoinLive{cfg: cfg, rpc: NewRPCClient(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)}
}

func (a *BitcoinLive) ID() string                        { return "btc" }
func (a *BitcoinLive) Name() string                      { return "Bitcoin" }
func (a *BitcoinLive) RequiredConfirmations() uint64      { return 3 }
func (a *BitcoinLive) NativeAsset() string                { return "btc" }

func (a *BitcoinLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	return "", fmt.Errorf("BTC key derivation not implemented — use BIP-84 + hdkeychain")
}

func (a *BitcoinLive) ValidateAddress(address string) bool {
	if len(address) < 26 || len(address) > 62 {
		return false
	}
	return address[:3] == "bc1" || address[0] == '1' || address[0] == '3'
}

func (a *BitcoinLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	var utxos []struct {
		Amount float64 `json:"amount"`
	}
	if err := a.rpc.Call(ctx, "listunspent", &utxos, 0, 9999999, []string{address}); err != nil {
		return nil, err
	}
	total := big.NewInt(0)
	for _, u := range utxos {
		sats := new(big.Float).SetFloat64(u.Amount)
		sats.Mul(sats, new(big.Float).SetFloat64(1e8))
		s, _ := sats.Int(nil)
		total.Add(total, s)
	}
	return &types.Balance{Address: address, Asset: "btc", Amount: total, Decimals: 8, Human: fmtUnits(total, 8)}, nil
}

func (a *BitcoinLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	return nil, fmt.Errorf("bitcoin does not support tokens")
}

func (a *BitcoinLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	return &types.UnsignedTx{
		ChainID: "btc",
		Metadata: map[string]interface{}{
			"from": req.From, "to": req.To, "amount": req.Amount.String(),
		},
	}, nil
}

func (a *BitcoinLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	return nil, fmt.Errorf("BTC signing not implemented — use btcd PSBT")
}

func (a *BitcoinLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	rawHex := fmt.Sprintf("%x", signed.RawBytes)
	var txHash string
	if err := a.rpc.Call(ctx, "sendrawtransaction", &txHash, rawHex); err != nil {
		return "", err
	}
	return txHash, nil
}

func (a *BitcoinLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	var count uint64
	if err := a.rpc.Call(ctx, "getblockcount", &count); err != nil {
		return 0, err
	}
	return count, nil
}

func (a *BitcoinLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	var hash string
	if err := a.rpc.Call(ctx, "getblockhash", &hash, blockNum); err != nil {
		return nil, err
	}
	var block struct {
		Time int64 `json:"time"`
		Tx   []struct {
			Txid string `json:"txid"`
			Vout []struct {
				Value        float64 `json:"value"`
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
			if vout.Value <= 0 {
				continue
			}
			addr := vout.ScriptPubKey.Address
			if addr == "" && len(vout.ScriptPubKey.Addresses) > 0 {
				addr = vout.ScriptPubKey.Addresses[0]
			}
			if addr == "" {
				continue
			}
			sats := new(big.Float).SetFloat64(vout.Value)
			sats.Mul(sats, new(big.Float).SetFloat64(1e8))
			s, _ := sats.Int(nil)
			transfers = append(transfers, types.DetectedTransfer{
				TxHash: tx.Txid, BlockNumber: blockNum, BlockHash: hash,
				To: addr, Amount: s, Asset: "btc", Timestamp: blockTime,
			})
		}
	}
	return transfers, nil
}
