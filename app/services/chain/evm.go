package chain

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

// ---------------------------------------------------------------------------
// EVMConfig — all that differs between EVM chains.
// ETH, Polygon, Arbitrum, Base, etc. = same adapter, different config.
// ---------------------------------------------------------------------------

type EVMConfig struct {
	ChainIDStr    string
	ChainName     string
	NativeSymbol  string
	NativeDecimal uint8
	NetworkID     int64
	RPCURL        string
	Confirmations uint64
	// ERC20Tokens lists registered ERC-20s for deposit log matching (from DB at boot).
	ERC20Tokens []types.Token
}

// ---------------------------------------------------------------------------
// EVMLive — production EVM adapter with real RPC calls.
// ---------------------------------------------------------------------------

type EVMLive struct {
	cfg EVMConfig
	rpc *RPCClient
}

func NewEVMLive(cfg EVMConfig) *EVMLive {
	return &EVMLive{
		cfg: cfg,
		rpc: NewRPCClient(cfg.RPCURL, "", ""),
	}
}

func (a *EVMLive) ID() string                        { return a.cfg.ChainIDStr }
func (a *EVMLive) Name() string                      { return a.cfg.ChainName }
func (a *EVMLive) RequiredConfirmations() uint64      { return a.cfg.Confirmations }
func (a *EVMLive) NativeAsset() string                { return a.cfg.NativeSymbol }

func (a *EVMLive) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	// TODO: BIP-44 m/44'/60'/0'/0/{index} via hdkeychain
	return "", fmt.Errorf("EVM key derivation not implemented — use go-ethereum/crypto + hdkeychain")
}

func (a *EVMLive) ValidateAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	for _, c := range address[2:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (a *EVMLive) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	var hexBal string
	if err := a.rpc.Call(ctx, "eth_getBalance", &hexBal, address, "latest"); err != nil {
		return nil, err
	}
	bal := hexToBigInt(hexBal)
	return &types.Balance{Address: address, Asset: a.cfg.NativeSymbol, Amount: bal, Decimals: a.cfg.NativeDecimal, Human: fmtUnits(bal, a.cfg.NativeDecimal)}, nil
}

func (a *EVMLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	data := "0x70a08231" + padAddr(address) // balanceOf(address)
	var hexResult string
	if err := a.rpc.Call(ctx, "eth_call", &hexResult, map[string]string{"to": token.Contract, "data": data}, "latest"); err != nil {
		return nil, err
	}
	bal := hexToBigInt(hexResult)
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: bal, Decimals: token.Decimals, Human: fmtUnits(bal, token.Decimals)}, nil
}

func (a *EVMLive) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	// Resolve nonce
	var hexNonce string
	if err := a.rpc.Call(ctx, "eth_getTransactionCount", &hexNonce, req.From, "pending"); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	// Gas price
	var hexGas string
	if err := a.rpc.Call(ctx, "eth_gasPrice", &hexGas); err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}

	var txData []byte
	to := req.To
	value := req.Amount
	gasLimit := uint64(21000)

	if req.Token != nil {
		txData = encodeERC20Transfer(req.To, req.Amount)
		to = req.Token.Contract
		value = big.NewInt(0)
		gasLimit = 65000
	}

	return &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"nonce":     hexToUint64(hexNonce),
			"to":        to,
			"value":     value.String(),
			"gas_limit": gasLimit,
			"gas_price": hexToBigInt(hexGas).String(),
			"chain_id":  a.cfg.NetworkID,
			"data":      txData,
		},
	}, nil
}

func (a *EVMLive) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	// TODO: RLP encode + secp256k1 sign with EIP-155 replay protection
	return nil, fmt.Errorf("EVM signing not implemented — use go-ethereum/types.SignTx")
}

func (a *EVMLive) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	rawHex := "0x" + hex.EncodeToString(signed.RawBytes)
	var txHash string
	if err := a.rpc.Call(ctx, "eth_sendRawTransaction", &txHash, rawHex); err != nil {
		return "", err
	}
	return txHash, nil
}

func (a *EVMLive) GetLatestBlock(ctx context.Context) (uint64, error) {
	var hexBlock string
	if err := a.rpc.Call(ctx, "eth_blockNumber", &hexBlock); err != nil {
		return 0, err
	}
	return hexToUint64(hexBlock), nil
}

func (a *EVMLive) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	hexBlock := fmt.Sprintf("0x%x", blockNum)
	var block struct {
		Hash         string `json:"hash"`
		Timestamp    string `json:"timestamp"`
		Transactions []struct {
			Hash  string `json:"hash"`
			From  string `json:"from"`
			To    string `json:"to"`
			Value string `json:"value"`
		} `json:"transactions"`
	}
	if err := a.rpc.Call(ctx, "eth_getBlockByNumber", &block, hexBlock, true); err != nil {
		return nil, err
	}

	blockTime := time.Unix(int64(hexToUint64(block.Timestamp)), 0)
	var transfers []types.DetectedTransfer

	// Native transfers
	for _, tx := range block.Transactions {
		val := hexToBigInt(tx.Value)
		if val.Sign() > 0 {
			transfers = append(transfers, types.DetectedTransfer{
				TxHash: tx.Hash, BlockNumber: blockNum, BlockHash: block.Hash,
				From: tx.From, To: tx.To, Amount: val,
				Asset: a.cfg.NativeSymbol, Timestamp: blockTime,
			})
		}
	}

	// ERC-20 Transfer events
	tokens := a.scanERC20(ctx, blockNum, block.Hash, blockTime)
	transfers = append(transfers, tokens...)

	return transfers, nil
}

func (a *EVMLive) scanERC20(ctx context.Context, blockNum uint64, blockHash string, blockTime time.Time) []types.DetectedTransfer {
	// Caller must have registered tokens in the registry — we access via package-level
	// In production, inject registry into adapter. For POC, accept this coupling.
	transferTopic := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	hexBlock := fmt.Sprintf("0x%x", blockNum)

	var logs []struct {
		Address         string   `json:"address"`
		Topics          []string `json:"topics"`
		Data            string   `json:"data"`
		TransactionHash string   `json:"transactionHash"`
		LogIndex        string   `json:"logIndex"`
	}

	// Single getLogs call for the entire block, no address filter
	// We'll match against known token contracts in Go
	_ = a.rpc.Call(ctx, "eth_getLogs", &logs, map[string]interface{}{
		"fromBlock": hexBlock, "toBlock": hexBlock,
		"topics": []string{transferTopic},
	})

	var result []types.DetectedTransfer
	for _, log := range logs {
		if len(log.Topics) < 3 {
			continue
		}
		from := topicToAddr(log.Topics[1])
		to := topicToAddr(log.Topics[2])
		amount := hexToBigInt(log.Data)

		// Match against known tokens — O(n) but n is small (2-4 tokens per chain)
		contractLower := strings.ToLower(log.Address)
		for _, t := range a.cfg.ERC20Tokens {
			if t.ChainID == a.cfg.ChainIDStr && strings.ToLower(t.Contract) == contractLower {
				tokenCopy := t
				result = append(result, types.DetectedTransfer{
					TxHash: log.TransactionHash, BlockNumber: blockNum, BlockHash: blockHash,
					From: from, To: to, Amount: amount,
					Asset: t.Symbol, Token: &tokenCopy,
					LogIndex: uint(hexToUint64(log.LogIndex)), Timestamp: blockTime,
				})
				break
			}
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Shared EVM helpers
// ---------------------------------------------------------------------------

var erc20Selector = []byte{0xa9, 0x05, 0x9c, 0xbb} // transfer(address,uint256)

func encodeERC20Transfer(to string, amount *big.Int) []byte {
	data := make([]byte, 68)
	copy(data[0:4], erc20Selector)
	addr, _ := hex.DecodeString(strings.TrimPrefix(to, "0x"))
	copy(data[16:36], addr)
	amtBytes := amount.Bytes()
	copy(data[68-len(amtBytes):68], amtBytes)
	return data
}

func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	if s == "" || s == "0" {
		return big.NewInt(0)
	}
	n := new(big.Int)
	n.SetString(s, 16)
	return n
}

func hexToUint64(s string) uint64 {
	return hexToBigInt(s).Uint64()
}

func padAddr(addr string) string {
	return fmt.Sprintf("%064s", strings.TrimPrefix(strings.ToLower(addr), "0x"))
}

func topicToAddr(topic string) string {
	t := strings.TrimPrefix(topic, "0x")
	if len(t) >= 40 {
		return "0x" + t[len(t)-40:]
	}
	return "0x" + t
}

func fmtUnits(amount *big.Int, decimals uint8) string {
	if amount == nil {
		return "0"
	}
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Div(amount, d)
	frac := new(big.Int).Mod(amount, d)
	if frac.Sign() == 0 {
		return whole.String()
	}
	fracStr := strings.TrimRight(fmt.Sprintf("%0*s", decimals, frac.String()), "0")
	return whole.String() + "." + fracStr
}
