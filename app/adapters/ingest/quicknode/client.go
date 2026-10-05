package quicknode

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/numeric"
)

const (
	quicknodeAPIBase                = "https://api.quicknode.com/streams/rest/v1"
	quicknodeDefaultSignatureHeader = "X-QN-Signature"
	quicknodeHTTPTimeout            = 30 * time.Second
	quicknodeDefaultNetwork         = "bitcoin-mainnet"
	// quicknodeBTCDecimals converts a BTC amount to satoshis.
	quicknodeBTCDecimals = 8
)

// QuickNodeProvider manages QuickNode Streams webhooks for Bitcoin block filtering.
type QuickNodeProvider struct {
	apiKey          string
	keyAtUse        providers.KeySource
	client          *httpclient.Client
	signatureHeader string
}

// NewQuickNodeProvider returns a provider that calls QuickNode Streams REST with apiKey (x-api-key).
func NewQuickNodeProvider(apiKey string) *QuickNodeProvider {
	return &QuickNodeProvider{
		apiKey:          apiKey,
		client:          httpclient.NewClient(quicknodeHTTPTimeout),
		signatureHeader: quicknodeDefaultSignatureHeader,
	}
}

// UseKeySource reads the credential on each call. The constructor key is
// not the one used after this is set, so boot does not capture it.
func (q *QuickNodeProvider) UseKeySource(source providers.KeySource) *QuickNodeProvider {
	if q == nil {
		return nil
	}
	q.keyAtUse = source
	return q
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

func (q *QuickNodeProvider) CreateWebhook(ctx context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error) {
	headers, headerErr := q.apiHeaders(ctx)
	if headerErr != nil {
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

	status, respBody, err := exchange(ctx, q.client, httpclient.MethodPost, quicknodeAPIBase+"/streams", headers, body)
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, fmt.Errorf("quicknode: build create request: %w", err)
		}
		return nil, fmt.Errorf("quicknode: create stream: %w", err)
	}
	if status != httpclient.StatusOK && status != httpclient.StatusCreated {
		return nil, fmt.Errorf("quicknode create stream: status %d: %s", status, respBody)
	}

	var result quicknodeCreateStreamResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("quicknode: decode create response: %w", err)
	}
	if strings.TrimSpace(result.ID) == "" {
		return nil, fmt.Errorf("quicknode: create stream: empty id in response")
	}

	return &providers.ProviderWebhook{
		ProviderWebhookID: result.ID,
		SigningSecret:     cfg.AuthSecret,
	}, nil
}

func streamDisplayName(cfg providers.ProviderConfig) string {
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
	headers, headerErr := q.apiHeaders(ctx)
	if headerErr != nil {
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
	status, respBody, err := exchange(ctx, q.client, httpclient.MethodPatch, u, headers, body)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("quicknode: build patch request: %w", err)
		}
		return fmt.Errorf("quicknode: patch stream: %w", err)
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return fmt.Errorf("quicknode patch stream: status %d: %s", status, respBody)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

func (q *QuickNodeProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	headers, headerErr := q.apiHeaders(ctx)
	if headerErr != nil {
		return fmt.Errorf("quicknode: empty API key")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("quicknode: empty webhook id")
	}

	u := fmt.Sprintf("%s/streams/%s", quicknodeAPIBase, id)
	status, respBody, err := exchange(ctx, q.client, httpclient.MethodDelete, u, headers, nil)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("quicknode: build delete request: %w", err)
		}
		return fmt.Errorf("quicknode: delete stream: %w", err)
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return fmt.Errorf("quicknode delete stream: status %d: %s", status, respBody)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — HMAC-SHA256 over raw body, hex digest (same pattern as Alchemy)
// ---------------------------------------------------------------------------

func (q *QuickNodeProvider) VerifyInbound(headers providers.Header, body []byte, secret string) (bool, error) {
	if err := providers.GateInboundCredential(context.Background(), q.keyAtUse); err != nil {
		return false, err
	}
	if err := providers.RejectBlankSigningSecret(secret); err != nil {
		return false, err
	}
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
	Txid        string          `json:"txid"`
	BlockNumber uint64          `json:"blockNumber"`
	BlockHash   string          `json:"blockHash"`
	ToAddress   string          `json:"toAddress"`
	Amount      decimal.Decimal `json:"amount"`
	Timestamp   int64           `json:"timestamp"`
}

func (q *QuickNodeProvider) ParsePayload(body []byte) ([]providers.InboundTransfer, error) {
	var items []quicknodeTransferItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("quicknode: unmarshal payload: %w", err)
	}

	out := make([]providers.InboundTransfer, 0, len(items))
	for i, item := range items {
		t, err := quicknodeItemToTransfer(item)
		if err != nil {
			return nil, fmt.Errorf("quicknode: item %d: %w", i, err)
		}
		out = append(out, t)
	}
	return out, nil
}

func quicknodeItemToTransfer(item quicknodeTransferItem) (providers.InboundTransfer, error) {
	if strings.TrimSpace(item.Txid) == "" {
		return providers.InboundTransfer{}, fmt.Errorf("empty txid")
	}
	if strings.TrimSpace(item.ToAddress) == "" {
		return providers.InboundTransfer{}, fmt.Errorf("empty toAddress")
	}

	if item.Amount.IsNegative() {
		return providers.InboundTransfer{}, fmt.Errorf("negative amount")
	}
	amount, err := numeric.ToBaseUnits(item.Amount, quicknodeBTCDecimals)
	if err != nil {
		return providers.InboundTransfer{}, fmt.Errorf("amount %s: %w", item.Amount.String(), err)
	}

	ts := time.Unix(item.Timestamp, 0)
	if item.Timestamp < 0 {
		return providers.InboundTransfer{}, fmt.Errorf("invalid timestamp %d", item.Timestamp)
	}

	return providers.InboundTransfer{
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

func (q *QuickNodeProvider) apiHeaders(ctx context.Context) (map[string]string, error) {
	key, err := providers.CredentialForCall(ctx, q.keyAtUse, q.apiKey)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"Content-Type": "application/json",
		"x-api-key":    key,
	}, nil
}

func exchange(ctx context.Context, client *httpclient.Client, method, rawURL string, header map[string]string, body []byte) (int, []byte, error) {
	resp, err := client.Do(ctx, httpclient.Request{
		Method:  method,
		URL:     rawURL,
		Header:  header,
		Body:    body,
		HasBody: body != nil,
	})
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, resp.Body, nil
}

// compile-time interface check
var _ providers.WebhookProvider = (*QuickNodeProvider)(nil)
