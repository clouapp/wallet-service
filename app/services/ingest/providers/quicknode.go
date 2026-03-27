package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	quicknodeAPIBase                = "https://api.quicknode.com/streams/rest/v1"
	quicknodeDefaultSignatureHeader = "X-QN-Signature"
	quicknodeHTTPTimeout            = 30 * time.Second
	quicknodeDefaultNetwork         = "bitcoin-mainnet"
)

// QuickNodeProvider manages QuickNode Streams webhooks for Bitcoin block filtering.
type QuickNodeProvider struct {
	apiKey          string
	client          *http.Client
	signatureHeader string
}

// NewQuickNodeProvider returns a provider that calls QuickNode Streams REST with apiKey (x-api-key).
func NewQuickNodeProvider(apiKey string) *QuickNodeProvider {
	return &QuickNodeProvider{
		apiKey:          apiKey,
		client:          &http.Client{Timeout: quicknodeHTTPTimeout},
		signatureHeader: quicknodeDefaultSignatureHeader,
	}
}

// SignatureHeader returns the HTTP header name used for inbound HMAC verification.
func (q *QuickNodeProvider) SignatureHeader() string {
	if q.signatureHeader == "" {
		return quicknodeDefaultSignatureHeader
	}
	return q.signatureHeader
}

// SetSignatureHeader overrides the inbound signature header (e.g. if QuickNode changes docs).
func (q *QuickNodeProvider) SetSignatureHeader(name string) {
	q.signatureHeader = strings.TrimSpace(name)
}

func (q *QuickNodeProvider) ProviderName() string {
	return "quicknode"
}

// ---------------------------------------------------------------------------
// CreateWebhook
// ---------------------------------------------------------------------------

type quicknodeDestination struct {
	URL              string            `json:"url"`
	Compression      string            `json:"compression"`
	Headers          map[string]string `json:"headers"`
	MaxRetry         int               `json:"max_retry"`
	RetryIntervalSec int               `json:"retry_interval_sec"`
}

type quicknodeCreateStreamReq struct {
	Name           string               `json:"name"`
	Network        string               `json:"network"`
	Dataset        string               `json:"dataset"`
	FilterFunction string               `json:"filter_function"`
	Destination    quicknodeDestination `json:"destination"`
	Status         string               `json:"status"`
}

type quicknodeCreateStreamResp struct {
	ID string `json:"id"`
}

func (q *QuickNodeProvider) CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error) {
	if strings.TrimSpace(q.apiKey) == "" {
		return nil, fmt.Errorf("quicknode: empty API key")
	}
	if strings.TrimSpace(cfg.WebhookURL) == "" {
		return nil, fmt.Errorf("quicknode: empty WebhookURL")
	}

	network := strings.TrimSpace(cfg.Network)
	if network == "" {
		network = quicknodeDefaultNetwork
	}

	filterB64, err := buildFilterFunctionBase64(cfg.Addresses)
	if err != nil {
		return nil, err
	}

	payload := quicknodeCreateStreamReq{
		Name:           streamDisplayName(cfg),
		Network:        network,
		Dataset:        "block",
		FilterFunction: filterB64,
		Destination: quicknodeDestination{
			URL:              cfg.WebhookURL,
			Compression:      "none",
			Headers:          map[string]string{"Content-Type": "application/json"},
			MaxRetry:         3,
			RetryIntervalSec: 1,
		},
		Status: "active",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("quicknode: marshal create request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, quicknodeAPIBase+"/streams", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("quicknode: build create request: %w", err)
	}
	q.setAPIHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("quicknode: create stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, q.readAPIError("create stream", resp)
	}

	var result quicknodeCreateStreamResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("quicknode: decode create response: %w", err)
	}
	if strings.TrimSpace(result.ID) == "" {
		return nil, fmt.Errorf("quicknode: create stream: empty id in response")
	}

	return &ProviderWebhook{
		ProviderWebhookID: result.ID,
		SigningSecret:     cfg.AuthSecret,
	}, nil
}

func streamDisplayName(cfg ProviderConfig) string {
	chain := strings.TrimSpace(cfg.ChainID)
	if chain == "" {
		return "BTC Deposit Monitor"
	}
	return strings.ToUpper(chain) + " Deposit Monitor"
}

// ---------------------------------------------------------------------------
// SyncAddresses
// ---------------------------------------------------------------------------

type quicknodePatchStreamReq struct {
	FilterFunction string `json:"filter_function"`
}

func (q *QuickNodeProvider) SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error {
	if strings.TrimSpace(q.apiKey) == "" {
		return fmt.Errorf("quicknode: empty API key")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("quicknode: empty webhook id")
	}

	filterB64, err := buildFilterFunctionBase64(allAddresses)
	if err != nil {
		return err
	}

	body, err := json.Marshal(quicknodePatchStreamReq{FilterFunction: filterB64})
	if err != nil {
		return fmt.Errorf("quicknode: marshal patch request: %w", err)
	}

	u := fmt.Sprintf("%s/streams/%s", quicknodeAPIBase, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("quicknode: build patch request: %w", err)
	}
	q.setAPIHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("quicknode: patch stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return q.readAPIError("patch stream", resp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

func (q *QuickNodeProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	if strings.TrimSpace(q.apiKey) == "" {
		return fmt.Errorf("quicknode: empty API key")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("quicknode: empty webhook id")
	}

	u := fmt.Sprintf("%s/streams/%s", quicknodeAPIBase, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("quicknode: build delete request: %w", err)
	}
	q.setAPIHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("quicknode: delete stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return q.readAPIError("delete stream", resp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — HMAC-SHA256 over raw body, hex digest (same pattern as Alchemy)
// ---------------------------------------------------------------------------

func (q *QuickNodeProvider) VerifyInbound(headers http.Header, body []byte, secret string) (bool, error) {
	hdr := q.SignatureHeader()
	sig := headers.Get(hdr)
	if sig == "" {
		return false, fmt.Errorf("quicknode: missing %s header", hdr)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}

// ---------------------------------------------------------------------------
// ParsePayload — filtered array from QuickNode JS filter
// ---------------------------------------------------------------------------

type quicknodeTransferItem struct {
	Txid        string  `json:"txid"`
	BlockNumber uint64  `json:"blockNumber"`
	BlockHash   string  `json:"blockHash"`
	ToAddress   string  `json:"toAddress"`
	Amount      float64 `json:"amount"`
	Timestamp   int64   `json:"timestamp"`
}

func (q *QuickNodeProvider) ParsePayload(body []byte) ([]InboundTransfer, error) {
	var items []quicknodeTransferItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("quicknode: unmarshal payload: %w", err)
	}

	out := make([]InboundTransfer, 0, len(items))
	for i, item := range items {
		t, err := quicknodeItemToTransfer(item)
		if err != nil {
			return nil, fmt.Errorf("quicknode: item %d: %w", i, err)
		}
		out = append(out, t)
	}
	return out, nil
}

func quicknodeItemToTransfer(item quicknodeTransferItem) (InboundTransfer, error) {
	if strings.TrimSpace(item.Txid) == "" {
		return InboundTransfer{}, fmt.Errorf("empty txid")
	}
	if strings.TrimSpace(item.ToAddress) == "" {
		return InboundTransfer{}, fmt.Errorf("empty toAddress")
	}

	amount := btcFloatToSatoshis(item.Amount)
	if amount.Sign() < 0 {
		return InboundTransfer{}, fmt.Errorf("negative amount")
	}

	ts := time.Unix(item.Timestamp, 0)
	if item.Timestamp < 0 {
		return InboundTransfer{}, fmt.Errorf("invalid timestamp %d", item.Timestamp)
	}

	return InboundTransfer{
		TxHash:      item.Txid,
		BlockNumber: item.BlockNumber,
		BlockHash:   item.BlockHash,
		From:        "",
		To:          item.ToAddress,
		Amount:      amount,
		Asset:       "BTC",
		Token:       nil,
		LogIndex:    -1,
		Timestamp:   ts,
	}, nil
}

func btcFloatToSatoshis(btc float64) *big.Int {
	if math.IsNaN(btc) || math.IsInf(btc, 0) {
		return big.NewInt(0)
	}
	sats := math.Round(btc * 1e8)
	if sats > float64(math.MaxInt64) || sats < float64(math.MinInt64) {
		// Unrealistic for BTC; clamp to zero to avoid undefined behavior
		return big.NewInt(0)
	}
	return big.NewInt(int64(sats))
}

// ---------------------------------------------------------------------------
// buildFilterFunction — JS filter source, base64-encoded for API filter_function
// ---------------------------------------------------------------------------

func buildFilterFunction(addresses []string) string {
	b64, err := buildFilterFunctionBase64(addresses)
	if err != nil {
		return ""
	}
	return b64
}

func buildFilterFunctionBase64(addresses []string) (string, error) {
	addrMap := make(map[string]bool, len(addresses))
	for _, a := range addresses {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		addrMap[a] = true
	}

	jsonMap, err := json.Marshal(addrMap)
	if err != nil {
		return "", fmt.Errorf("quicknode: marshal address map: %w", err)
	}

	js := `function main(stream) {
  var addresses = ` + string(jsonMap) + `;
  var block = stream.data[0];
  var results = [];
  var txs = block.tx || [];
  for (var i = 0; i < txs.length; i++) {
    var tx = txs[i];
    var vouts = tx.vout || [];
    for (var j = 0; j < vouts.length; j++) {
      var vout = vouts[j];
      var addr = vout.scriptPubKey && (vout.scriptPubKey.address || (vout.scriptPubKey.addresses && vout.scriptPubKey.addresses[0]));
      if (addr && addresses[addr]) {
        results.push({txid: tx.txid, blockNumber: block.height, blockHash: block.hash, toAddress: addr, amount: vout.value, timestamp: block.time});
      }
    }
  }
  return results.length > 0 ? results : null;
}
`
	return base64.StdEncoding.EncodeToString([]byte(js)), nil
}

func (q *QuickNodeProvider) setAPIHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", q.apiKey)
}

func (q *QuickNodeProvider) readAPIError(op string, resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("quicknode %s: status %d: %s", op, resp.StatusCode, string(b))
}

var _ WebhookProvider = (*QuickNodeProvider)(nil)
