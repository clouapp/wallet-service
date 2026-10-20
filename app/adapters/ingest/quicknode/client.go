package quicknode

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/httpclient"
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

// quicknodeDestinationAttributes is the webhook a stream posts to (WebhookAttributes:
// every field is required). The deliveries are not compressed: the signature covers
// the uncompressed body. SecurityToken is the key the stream signs them with.
type quicknodeDestinationAttributes struct {
	URL              string            `json:"url"`
	SecurityToken    string            `json:"security_token"`
	Compression      string            `json:"compression"`
	Headers          map[string]string `json:"headers"`
	MaxRetry         int               `json:"max_retry"`
	RetryIntervalSec int               `json:"retry_interval_sec"`
	PostTimeoutSec   int               `json:"post_timeout_sec"`
}

// quicknodeCreateStreamReq is CreateStreamDto with every required field.
type quicknodeCreateStreamReq struct {
	Name                  string                         `json:"name"`
	Network               string                         `json:"network"`
	Dataset               string                         `json:"dataset"`
	FilterFunction        string                         `json:"filter_function"`
	Region                string                         `json:"region"`
	DatasetBatchSize      int                            `json:"dataset_batch_size"`
	ElasticBatchEnabled   bool                           `json:"elastic_batch_enabled"`
	Destination           string                         `json:"destination"`
	DestinationAttributes quicknodeDestinationAttributes `json:"destination_attributes"`
	Status                string                         `json:"status"`
}

// quicknodeCreateStreamResp is the created stream: its id, and the security token
// QuickNode signs the stream's deliveries with.
type quicknodeCreateStreamResp struct {
	ID                    string `json:"id"`
	DestinationAttributes struct {
		SecurityToken string `json:"security_token"`
	} `json:"destination_attributes"`
}

const (
	quicknodeRegion       = "usa_east"
	quicknodeStatusPaused = "paused"
	quicknodeTokenBytes   = 32
	quicknodeListPageSize = 100
)

// CreateWebhook creates the stream paused, with a security token of its own, so it
// pushes only once its subscription is stored (ActivateWebhook). The signing secret is
// the token the answer carries, else the one sent. A created stream whose answer
// cannot be read is found by its name and deleted.
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
	token, err := randomHex(quicknodeTokenBytes)
	if err != nil {
		return nil, fmt.Errorf("quicknode: generate the security token: %w", err)
	}
	name, err := streamDisplayName(cfg)
	if err != nil {
		return nil, err
	}

	payload := quicknodeCreateStreamReq{
		Name:                name,
		Network:             network,
		Dataset:             "block",
		FilterFunction:      filterB64,
		Region:              quicknodeRegion,
		DatasetBatchSize:    1,
		ElasticBatchEnabled: false,
		Destination:         "webhook",
		DestinationAttributes: quicknodeDestinationAttributes{
			URL:              cfg.WebhookURL,
			SecurityToken:    token,
			Compression:      "none",
			Headers:          map[string]string{},
			MaxRetry:         3,
			RetryIntervalSec: 1,
			PostTimeoutSec:   10,
		},
		Status: quicknodeStatusPaused,
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
		return nil, chain.Unavailable(fmt.Errorf("quicknode: create stream: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusCreated {
		return nil, chain.FromProviderHTTP(status, string(respBody))
	}

	var result quicknodeCreateStreamResp
	if err := json.Unmarshal(respBody, &result); err != nil || strings.TrimSpace(result.ID) == "" {
		return nil, q.deleteUnread(ctx, name)
	}
	secret := strings.TrimSpace(result.DestinationAttributes.SecurityToken)
	if secret == "" {
		secret = token
	}
	return &providers.ProviderWebhook{ProviderWebhookID: result.ID, SigningSecret: secret}, nil
}

// deleteUnread deletes the stream named name, created by a call whose answer could
// not be read; it fails either way, naming the stream when it was not found.
func (q *QuickNodeProvider) deleteUnread(ctx context.Context, name string) error {
	id, err := q.findStream(ctx, name)
	if err != nil || id == "" {
		return fmt.Errorf("quicknode: the create answer could not be read and stream %q was not found; delete it in the QuickNode dashboard", name)
	}
	if err := q.DeleteWebhook(ctx, id); err != nil {
		return fmt.Errorf("quicknode: the create answer could not be read and stream %q (%s) was not deleted: %w", name, id, err)
	}
	return fmt.Errorf("quicknode: the create answer could not be read; stream %q was deleted", name)
}

// findStream returns the id of the stream named name, "" when no listed stream has it.
func (q *QuickNodeProvider) findStream(ctx context.Context, name string) (string, error) {
	headers, err := q.apiHeaders(ctx)
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/streams?limit=%d&offset=0", quicknodeAPIBase, quicknodeListPageSize)
	status, respBody, err := exchange(ctx, q.client, httpclient.MethodGet, u, headers, nil)
	if err != nil {
		return "", err
	}
	if status != httpclient.StatusOK {
		return "", chain.FromProviderHTTP(status, string(respBody))
	}
	type stream struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var page struct {
		Data []stream `json:"data"`
	}
	if err := json.Unmarshal(respBody, &page); err != nil {
		var list []stream
		if json.Unmarshal(respBody, &list) != nil {
			return "", fmt.Errorf("quicknode: decode stream list: %w", err)
		}
		page.Data = list
	}
	for _, listed := range page.Data {
		if listed.Name == name {
			return listed.ID, nil
		}
	}
	return "", nil
}

// ActivateWebhook starts a stream created paused.
func (q *QuickNodeProvider) ActivateWebhook(ctx context.Context, webhookID string) error {
	headers, err := q.apiHeaders(ctx)
	if err != nil {
		return fmt.Errorf("quicknode: empty API key")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("quicknode: empty webhook id")
	}
	status, respBody, err := exchange(ctx, q.client, httpclient.MethodPost, quicknodeAPIBase+"/streams/"+url.PathEscape(id)+"/activate", headers, []byte("{}"))
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("quicknode: build activate request: %w", err)
		}
		return chain.Unavailable(fmt.Errorf("quicknode: activate stream: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusCreated && status != httpclient.StatusNoContent {
		return chain.FromProviderHTTP(status, string(respBody))
	}
	return nil
}

// streamDisplayName names the stream after its chain, with a random suffix so a stream
// whose create answer is lost can be found.
func streamDisplayName(cfg providers.ProviderConfig) (string, error) {
	chainID := strings.TrimSpace(cfg.ChainID)
	if chainID == "" {
		chainID = "btc"
	}
	suffix, err := randomHex(4)
	if err != nil {
		return "", fmt.Errorf("quicknode: name the stream: %w", err)
	}
	return strings.ToUpper(chainID) + " Deposit Monitor " + suffix, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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
		return chain.Unavailable(fmt.Errorf("quicknode: patch stream: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return chain.FromProviderHTTP(status, string(respBody))
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
		return chain.Unavailable(fmt.Errorf("quicknode: delete stream: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return chain.FromProviderHTTP(status, string(respBody))
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — HMAC-SHA256 of nonce + timestamp + body, keyed with the stream's
// security token, hex (QuickNode Streams' scheme)
// ---------------------------------------------------------------------------

const (
	quicknodeNonceHeader     = "X-QN-Nonce"
	quicknodeTimestampHeader = "X-QN-Timestamp"
	// quicknodeReplayWindow is how far a delivery's signed timestamp may be from now:
	// a captured delivery sent again later is refused.
	quicknodeReplayWindow = 5 * time.Minute
)

// withinReplayWindow refuses a signed timestamp (Unix seconds, or milliseconds)
// further than quicknodeReplayWindow from now.
func withinReplayWindow(timestamp string, now time.Time) error {
	value, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return fmt.Errorf("quicknode: %s is not a Unix time", quicknodeTimestampHeader)
	}
	signed := time.Unix(value, 0)
	if value > 1_000_000_000_000 {
		signed = time.UnixMilli(value)
	}
	if skew := now.Sub(signed); skew > quicknodeReplayWindow || skew < -quicknodeReplayWindow {
		return fmt.Errorf("quicknode: delivery signed %s from now, outside the %s replay window", skew.Round(time.Second), quicknodeReplayWindow)
	}
	return nil
}

func (q *QuickNodeProvider) VerifyInbound(headers providers.Header, body []byte, secret string) (bool, error) {
	if err := providers.GateInboundCredential(context.Background(), q.keyAtUse); err != nil {
		return false, err
	}
	if err := providers.RejectBlankSigningSecret(secret); err != nil {
		return false, err
	}
	signatureHeader := q.SignatureHeader()
	sig := headers.Get(signatureHeader)
	nonce := headers.Get(quicknodeNonceHeader)
	timestamp := headers.Get(quicknodeTimestampHeader)
	for name, value := range map[string]string{signatureHeader: sig, quicknodeNonceHeader: nonce, quicknodeTimestampHeader: timestamp} {
		if value == "" {
			return false, fmt.Errorf("quicknode: missing %s header", name)
		}
	}
	if err := withinReplayWindow(timestamp, time.Now()); err != nil {
		return false, err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce + timestamp))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}

// ---------------------------------------------------------------------------
// ParsePayload — filtered array from QuickNode JS filter
// ---------------------------------------------------------------------------

// quicknodeTransferItem is one paid output the stream's filter function reports; Vout
// is its index in the transaction, the position the deposit scanner gives it.
type quicknodeTransferItem struct {
	Txid        string          `json:"txid"`
	BlockNumber uint64          `json:"blockNumber"`
	BlockHash   string          `json:"blockHash"`
	ToAddress   string          `json:"toAddress"`
	Vout        *int            `json:"vout"`
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
			// Left to the deposit scanner (a stream whose filter predates the vout
			// sends none until its addresses are synced); the other items are kept.
			slog.Warn("quicknode: item skipped", "item", i, "tx", item.Txid, "error", err)
			continue
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
	if item.Vout == nil || *item.Vout < 0 {
		return providers.InboundTransfer{}, fmt.Errorf("missing or negative vout")
	}

	if item.Amount.IsNegative() {
		return providers.InboundTransfer{}, fmt.Errorf("negative amount")
	}

	ts := time.Unix(item.Timestamp, 0)
	if item.Timestamp < 0 {
		return providers.InboundTransfer{}, fmt.Errorf("invalid timestamp %d", item.Timestamp)
	}

	// The amount is a human decimal. The chain row's native decimals scale it
	// in the ingest service; this adapter does not guess satoshis.
	return providers.InboundTransfer{
		TxHash:        item.Txid,
		BlockNumber:   item.BlockNumber,
		BlockHash:     item.BlockHash,
		From:          "",
		To:            item.ToAddress,
		AmountIsHuman: true,
		HumanAmount:   item.Amount.String(),
		Asset:         "BTC",
		Token:         nil,
		LogIndex:      *item.Vout,
		Timestamp:     ts,
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
      if (addr && addresses[addr] && vout.value > 0) {
        results.push({txid: tx.txid, blockNumber: block.height, blockHash: block.hash, toAddress: addr, vout: j, amount: vout.value, timestamp: block.time});
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
