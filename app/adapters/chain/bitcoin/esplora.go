package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// esploraMaxResponseBytes bounds one Esplora body; a page of 25 large
	// transactions stays well below it.
	esploraMaxResponseBytes = 32 << 20
	// esploraBlockTxsPageSize is how many transactions GET /block/:hash/txs/:start
	// returns; start must be a multiple of it.
	esploraBlockTxsPageSize = 25
	btcTxIDHexLength        = 64

	// A rate-limited Esplora call waits at most ~3.5 s plus jitter before giving up,
	// so a block scan or a plan over many addresses stays bounded.
	esploraRateLimitMaxAttempts = 4
	esploraRateLimitBaseDelay   = 500 * time.Millisecond
	esploraRateLimitMaxDelay    = 4 * time.Second

	bitcoindTxNotFoundCode = -5
)

// esploraStatusError is a non-2xx Esplora answer that was not a rate limit.
type esploraStatusError struct {
	path   string
	status int
	body   string
}

func (e *esploraStatusError) Error() string {
	return fmt.Sprintf("esplora GET %s: HTTP %d: %s", e.path, e.status, e.body)
}

func esploraRetry() rateLimitRetry {
	return rateLimitRetry{
		maxAttempts: esploraRateLimitMaxAttempts,
		baseDelay:   esploraRateLimitBaseDelay,
		maxDelay:    esploraRateLimitMaxDelay,
		jitter:      randomJitter,
		sleep:       sleepContext,
	}
}

// esploraGet returns the body of a 2xx answer to GET {rpc_url}{path}. Rate-limited
// answers (HTTP 429/403) are retried with backoff; any other non-2xx is an
// *esploraStatusError. Errors never carry the base URL, which may embed an API key.
func (a *BitcoinLive) esploraGet(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("esplora path %q must start with /", path)
	}
	requestURL := strings.TrimRight(a.cfg.RPCURL, "/") + path
	for attempt := 1; ; attempt++ {
		status, header, body, err := a.esploraFetch(ctx, requestURL, path)
		if err != nil {
			return nil, chain.Unavailable(httpclient.RedactURL(err, requestURL))
		}
		if status >= httpclient.StatusOK && status < httpclient.StatusMultipleChoices {
			return body, nil
		}
		if !isRateLimited(status, body) {
			statusErr := &esploraStatusError{
				path:   path,
				status: status,
				body:   httpclient.RedactURLText(strings.TrimSpace(string(body)), requestURL),
			}
			return nil, chain.Wrap(chain.KindOrProvider(status, statusErr.body), statusErr)
		}
		if attempt >= a.esploraRetry.maxAttempts {
			return nil, httpclient.RedactURL(fmt.Errorf("esplora GET %s: %w (HTTP %d) after %d attempts", path, chain.ErrRateLimited, status, attempt), requestURL)
		}
		delay := a.esploraRetry.delay(attempt, header.Get("Retry-After"), time.Now())
		slog.Warn("esplora rate limited, backing off", "chain", a.cfg.ChainIDStr, "path", path, "status", status, "attempt", attempt, "delay", delay.String())
		if err := a.esploraRetry.sleep(ctx, delay); err != nil {
			return nil, httpclient.RedactURL(fmt.Errorf("esplora GET %s: %w", path, err), requestURL)
		}
	}
}

func (a *BitcoinLive) esploraFetch(ctx context.Context, requestURL, path string) (int, httpclient.Header, []byte, error) {
	resp, err := a.http.Do(ctx, httpclient.Request{
		Method:   httpclient.MethodGet,
		URL:      requestURL,
		MaxBytes: esploraMaxResponseBytes + 1,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, nil, nil, fmt.Errorf("build esplora GET %s: %w", path, withoutURL(err))
		}
		if httpclient.IsRead(err) {
			return 0, nil, nil, fmt.Errorf("read esplora GET %s: %w", path, withoutURL(err))
		}
		return 0, nil, nil, fmt.Errorf("esplora GET %s: %w", path, withoutURL(err))
	}
	if len(resp.Body) > esploraMaxResponseBytes {
		return 0, nil, nil, fmt.Errorf("esplora GET %s: response larger than %d bytes", path, esploraMaxResponseBytes)
	}
	return resp.StatusCode, resp.Header, resp.Body, nil
}

// apiKeyHeader carries the optional API key of a hosted provider (Tatum).
const apiKeyHeader = "x-api-key"

// esploraPost sends a text body to an Esplora path. The answer is refused past
// esploraMaxResponseBytes. Errors do not include the URL or an API key.
func (a *BitcoinLive) esploraPost(ctx context.Context, path, body string) (int, []byte, error) {
	if a == nil || a.cfg.RPCURL == "" {
		return 0, nil, fmt.Errorf("esplora POST %s: RPC URL is not configured", path)
	}
	requestURL := strings.TrimRight(a.cfg.RPCURL, "/") + path
	resp, err := a.http.Do(ctx, httpclient.Request{
		Method:   httpclient.MethodPost,
		URL:      requestURL,
		Header:   map[string]string{"Content-Type": "text/plain"},
		Body:     []byte(body),
		HasBody:  true,
		MaxBytes: esploraMaxResponseBytes + 1,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("esplora POST %s: %w", path, withoutURL(err))
	}
	if len(resp.Body) > esploraMaxResponseBytes {
		return 0, nil, fmt.Errorf("esplora POST %s: response larger than %d bytes", path, esploraMaxResponseBytes)
	}
	out := resp.Body
	if a.apiKey != "" {
		out = bytes.ReplaceAll(out, []byte(a.apiKey), []byte("[redacted]"))
	}
	return resp.StatusCode, out, nil
}

func isEsploraNotFound(err error) bool {
	var statusErr *esploraStatusError
	return errors.As(err, &statusErr) && statusErr.status == httpclient.StatusNotFound
}

func isBTCHash(value string) bool {
	if len(value) != btcTxIDHexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeBTCHash(value, kind string) (string, error) {
	hash := strings.ToLower(strings.TrimSpace(value))
	if !isBTCHash(hash) {
		return "", fmt.Errorf("btc %s %q is not a 64-character hex hash", kind, value)
	}
	return hash, nil
}

// ---------------------------------------------------------------------------
// Transaction confirmation
// ---------------------------------------------------------------------------

type esploraTxStatus struct {
	Confirmed   bool    `json:"confirmed"`
	BlockHeight *uint64 `json:"block_height"`
}

// getTransactionBlockREST reads GET /tx/:txid/status. Confirmed returns the block
// height; unconfirmed, and unknown to the indexer (HTTP 404, e.g. right after
// broadcast), return 0 so the confirmation loop keeps the transaction pending. Any
// other non-2xx or an unreadable body is an error.
func (a *BitcoinLive) getTransactionBlockREST(ctx context.Context, txHash string) (uint64, error) {
	txID, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return 0, err
	}
	body, err := a.esploraGet(ctx, "/tx/"+txID+"/status")
	if isEsploraNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var status esploraTxStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return 0, fmt.Errorf("parse tx status of %s: %w", txID, err)
	}
	if !status.Confirmed {
		return 0, nil
	}
	if status.BlockHeight == nil || *status.BlockHeight == 0 {
		return 0, fmt.Errorf("tx %s is confirmed but has no block height", txID)
	}
	return *status.BlockHeight, nil
}

// getTransactionBlockRPC asks bitcoind for the tx's block (getrawtransaction
// verbose), then the block's height (getblockheader). A tx bitcoind does not know
// (-5: mempool-only node without -txindex, or not relayed yet), one without a block,
// or one whose block left the main chain is still pending (0).
func (a *BitcoinLive) getTransactionBlockRPC(ctx context.Context, txHash string) (uint64, error) {
	txID, err := normalizeBTCHash(txHash, "txid")
	if err != nil {
		return 0, err
	}
	var tx struct {
		BlockHash string `json:"blockhash"`
	}
	if err := a.rpc.Call(ctx, "getrawtransaction", &tx, txID, true); err != nil {
		var rpcErr *rpc.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == bitcoindTxNotFoundCode {
			return 0, nil
		}
		return 0, err
	}
	if tx.BlockHash == "" {
		return 0, nil
	}
	blockHash, err := normalizeBTCHash(tx.BlockHash, "block hash")
	if err != nil {
		return 0, err
	}
	var header struct {
		Height        *uint64 `json:"height"`
		Confirmations int64   `json:"confirmations"`
	}
	if err := a.rpc.Call(ctx, "getblockheader", &header, blockHash, true); err != nil {
		return 0, err
	}
	if header.Confirmations < 1 {
		return 0, nil
	}
	if header.Height == nil || *header.Height == 0 {
		return 0, fmt.Errorf("block %s of tx %s has no height", blockHash, txID)
	}
	return *header.Height, nil
}

// ---------------------------------------------------------------------------
// Block scanning
// ---------------------------------------------------------------------------

type esploraBlock struct {
	ID        string `json:"id"`
	Height    uint64 `json:"height"`
	Timestamp int64  `json:"timestamp"`
	TxCount   int    `json:"tx_count"`
}

type esploraTx struct {
	Txid string `json:"txid"`
	Vout []struct {
		ScriptpubkeyAddress string `json:"scriptpubkey_address"`
		Value               int64  `json:"value"`
	} `json:"vout"`
}

// scanBlockREST reads every transaction of block blockNum: the hash
// (/block-height/:n), the header with tx_count (/block/:hash), then
// /block/:hash/txs/:start for start = 0, 25, 50, ... until tx_count transactions are
// read. Any failed or short page fails the scan instead of returning a partial block.
func (a *BitcoinLive) scanBlockREST(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	hashBody, err := a.esploraGet(ctx, fmt.Sprintf("/block-height/%d", blockNum))
	if err != nil {
		return nil, fmt.Errorf("fetch hash of block %d: %w", blockNum, err)
	}
	blockHash, err := normalizeBTCHash(string(hashBody), "block hash")
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", blockNum, err)
	}

	block, err := a.fetchEsploraBlock(ctx, blockNum, blockHash)
	if err != nil {
		return nil, err
	}
	txs, err := a.fetchEsploraBlockTxs(ctx, blockHash, block.TxCount)
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", blockNum, err)
	}
	return a.esploraTransfers(txs, blockNum, blockHash, time.Unix(block.Timestamp, 0)), nil
}

func (a *BitcoinLive) fetchEsploraBlock(ctx context.Context, blockNum uint64, blockHash string) (esploraBlock, error) {
	body, err := a.esploraGet(ctx, "/block/"+blockHash)
	if err != nil {
		return esploraBlock{}, fmt.Errorf("fetch block %d: %w", blockNum, err)
	}
	var block esploraBlock
	if err := json.Unmarshal(body, &block); err != nil {
		return esploraBlock{}, fmt.Errorf("parse block %d: %w", blockNum, err)
	}
	if !strings.EqualFold(block.ID, blockHash) {
		return esploraBlock{}, fmt.Errorf("block %d: header id %q does not match hash %s", blockNum, block.ID, blockHash)
	}
	if block.Height != blockNum {
		return esploraBlock{}, fmt.Errorf("block %s: header height %d, want %d", blockHash, block.Height, blockNum)
	}
	if block.TxCount < 1 {
		return esploraBlock{}, fmt.Errorf("block %d: tx_count %d, a block has at least its coinbase", blockNum, block.TxCount)
	}
	return block, nil
}

func (a *BitcoinLive) fetchEsploraBlockTxs(ctx context.Context, blockHash string, txCount int) ([]esploraTx, error) {
	txs := make([]esploraTx, 0, txCount)
	for start := 0; start < txCount; start += esploraBlockTxsPageSize {
		body, err := a.esploraGet(ctx, fmt.Sprintf("/block/%s/txs/%d", blockHash, start))
		if err != nil {
			return nil, fmt.Errorf("fetch txs from %d of %d: %w", start, txCount, err)
		}
		var page []esploraTx
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse txs from %d: %w", start, err)
		}
		want := min(esploraBlockTxsPageSize, txCount-start)
		if len(page) != want {
			return nil, fmt.Errorf("txs page from %d has %d transactions, want %d of %d", start, len(page), want, txCount)
		}
		txs = append(txs, page...)
	}
	return txs, nil
}

func (a *BitcoinLive) esploraTransfers(txs []esploraTx, blockNum uint64, blockHash string, blockTime time.Time) []types.DetectedTransfer {
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
	return transfers
}
