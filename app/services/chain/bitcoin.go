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

	"github.com/macrowallets/waas/pkg/types"
)

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
	cfg     BitcoinConfig
	rpc     *RPCClient
	restAPI bool
	http    *http.Client
}

func NewBitcoinLive(cfg BitcoinConfig) *BitcoinLive {
	isREST := strings.Contains(cfg.RPCURL, "blockstream.info") ||
		strings.Contains(cfg.RPCURL, "mempool.space")
	return &BitcoinLive{
		cfg:     cfg,
		rpc:     NewRPCClient(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass),
		restAPI: isREST,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *BitcoinLive) ID() string                    { return a.cfg.ChainIDStr }
func (a *BitcoinLive) Name() string                  { return a.cfg.ChainName }
func (a *BitcoinLive) RequiredConfirmations() uint64 { return a.cfg.Confirmations }
func (a *BitcoinLive) NativeAsset() string           { return a.cfg.NativeSymbol }

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

func (a *BitcoinLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	const estimatedVBytes = 140
	feeRateSatPerVByte := 10

	if a.cfg.FeeRateDefault > 0 {
		feeRateSatPerVByte = a.cfg.FeeRateDefault
	}

	feeSat := int64(estimatedVBytes * feeRateSatPerVByte)
	fee := new(big.Int).SetInt64(feeSat)

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
	return &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
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
				To: addr, Amount: s, Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
			})
		}
	}
	return transfers, nil
}

func (a *BitcoinLive) scanBlockREST(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	baseURL := strings.TrimRight(a.cfg.RPCURL, "/")

	hashReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/block-height/%d", baseURL, blockNum), nil)
	if err != nil {
		return nil, fmt.Errorf("build block hash request: %w", err)
	}
	hashResp, err := a.http.Do(hashReq)
	if err != nil {
		return nil, fmt.Errorf("fetch block hash: %w", err)
	}
	defer hashResp.Body.Close()
	hashBody, _ := io.ReadAll(hashResp.Body)
	if hashResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("block hash API returned %d: %s", hashResp.StatusCode, string(hashBody))
	}
	blockHash := strings.TrimSpace(string(hashBody))

	blockReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/block/%s", baseURL, blockHash), nil)
	if err != nil {
		return nil, fmt.Errorf("build block request: %w", err)
	}
	blockResp, err := a.http.Do(blockReq)
	if err != nil {
		return nil, fmt.Errorf("fetch block: %w", err)
	}
	defer blockResp.Body.Close()
	blockBody, _ := io.ReadAll(blockResp.Body)

	var blockInfo struct {
		ID        string `json:"id"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(blockBody, &blockInfo); err != nil {
		return nil, fmt.Errorf("parse block: %w", err)
	}

	txsReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/block/%s/txs", baseURL, blockHash), nil)
	if err != nil {
		return nil, fmt.Errorf("build txs request: %w", err)
	}
	txsResp, err := a.http.Do(txsReq)
	if err != nil {
		return nil, fmt.Errorf("fetch block txs: %w", err)
	}
	defer txsResp.Body.Close()
	txsBody, _ := io.ReadAll(txsResp.Body)

	var txs []struct {
		Txid string `json:"txid"`
		Vout []struct {
			ScriptpubkeyAddress string `json:"scriptpubkey_address"`
			Value               int64  `json:"value"`
		} `json:"vout"`
	}
	if err := json.Unmarshal(txsBody, &txs); err != nil {
		return nil, fmt.Errorf("parse block txs: %w", err)
	}

	blockTime := time.Unix(blockInfo.Timestamp, 0)
	var transfers []types.DetectedTransfer
	for _, tx := range txs {
		for _, vout := range tx.Vout {
			if vout.Value <= 0 || vout.ScriptpubkeyAddress == "" {
				continue
			}
			transfers = append(transfers, types.DetectedTransfer{
				TxHash: tx.Txid, BlockNumber: blockNum, BlockHash: blockHash,
				To: vout.ScriptpubkeyAddress, Amount: big.NewInt(vout.Value),
				Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
			})
		}
	}
	return transfers, nil
}
