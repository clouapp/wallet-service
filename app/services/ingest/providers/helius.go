package providers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	heliusAPIBase     = "https://api-mainnet.helius-rpc.com"
	heliusHTTPTimeout = 30 * time.Second
	// heliusMaxTokenDecimals bounds SPL mint decimals.
	heliusMaxTokenDecimals = 18

	heliusWebhookTypeMainnet = "enhanced"
	heliusWebhookTypeDevnet  = "enhancedDevnet"
)

type HeliusProvider struct {
	apiKey   string
	keyAtUse KeySource
	client   *httpclient.Client
}

func NewHeliusProvider(apiKey string) *HeliusProvider {
	return &HeliusProvider{
		apiKey: apiKey,
		client: httpclient.NewClient(heliusHTTPTimeout),
	}
}

// UseKeySource reads the credential on each call. The constructor key is
// not the one used after this is set, so boot does not capture it.
func (h *HeliusProvider) UseKeySource(source KeySource) *HeliusProvider {
	if h == nil {
		return nil
	}
	h.keyAtUse = source
	return h
}

func (h *HeliusProvider) ProviderName() string {
	return "helius"
}

// ---------------------------------------------------------------------------
// CreateWebhook
// ---------------------------------------------------------------------------

type heliusWebhookBody struct {
	WebhookURL       string   `json:"webhookURL"`
	WebhookType      string   `json:"webhookType"`
	AccountAddresses []string `json:"accountAddresses"`
	TransactionTypes []string `json:"transactionTypes"`
	AuthHeader       string   `json:"authHeader"`
}

type heliusCreateResp struct {
	WebhookID        string   `json:"webhookID"`
	WebhookURL       string   `json:"webhookURL"`
	WebhookType      string   `json:"webhookType"`
	AccountAddresses []string `json:"accountAddresses"`
	TransactionTypes []string `json:"transactionTypes"`
	AuthHeader       string   `json:"authHeader"`
	Active           bool     `json:"active"`
}

func (h *HeliusProvider) CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error) {
	key, err := h.apiKeyFor(ctx)
	if err != nil {
		return nil, err
	}
	webhookURL := strings.TrimSpace(cfg.WebhookURL)
	if webhookURL == "" {
		return nil, fmt.Errorf("helius: WebhookURL is required")
	}

	authHeader, err := resolveHeliusAuthHeader(cfg.AuthSecret)
	if err != nil {
		return nil, err
	}

	body := heliusWebhookBody{
		WebhookURL:       webhookURL,
		WebhookType:      heliusWebhookTypeForNetwork(cfg.Network),
		AccountAddresses: append([]string(nil), cfg.Addresses...),
		TransactionTypes: []string{"ANY"},
		AuthHeader:       authHeader,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("helius: marshal create request: %w", err)
	}

	status, respBody, err := exchange(ctx, h.client, httpclient.MethodPost, h.endpointURL("/v0/webhooks", key), heliusJSONHeaders(), raw)
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, fmt.Errorf("helius: build create request: %w", err)
		}
		return nil, fmt.Errorf("helius: create webhook call: %w", err)
	}
	if status != httpclient.StatusOK {
		return nil, fmt.Errorf("helius create webhook: status %d: %s", status, respBody)
	}

	var result heliusCreateResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("helius: decode create response: %w", err)
	}

	if strings.TrimSpace(result.WebhookID) == "" {
		return nil, fmt.Errorf("helius: create response missing webhookID")
	}

	return &ProviderWebhook{
		ProviderWebhookID: result.WebhookID,
		SigningSecret:     authHeader,
	}, nil
}

// ---------------------------------------------------------------------------
// SyncAddresses — GET current webhook, PUT full replacement
// ---------------------------------------------------------------------------

func (h *HeliusProvider) SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error {
	key, err := h.apiKeyFor(ctx)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("helius: webhookID is required")
	}

	getStatus, getBody, err := exchange(ctx, h.client, httpclient.MethodGet, h.endpointURL("/v0/webhooks/"+url.PathEscape(id), key), nil, nil)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("helius: build get webhook request: %w", err)
		}
		return fmt.Errorf("helius: get webhook call: %w", err)
	}
	if getStatus != httpclient.StatusOK {
		return fmt.Errorf("helius get webhook: status %d: %s", getStatus, getBody)
	}

	var current heliusCreateResp
	if err := json.Unmarshal(getBody, &current); err != nil {
		return fmt.Errorf("helius: decode get webhook response: %w", err)
	}

	putBody := heliusWebhookBody{
		WebhookURL:       current.WebhookURL,
		WebhookType:      current.WebhookType,
		AccountAddresses: append([]string(nil), allAddresses...),
		TransactionTypes: []string{"ANY"},
		AuthHeader:       current.AuthHeader,
	}
	if len(current.WebhookURL) == 0 || len(current.WebhookType) == 0 || len(current.AuthHeader) == 0 {
		return fmt.Errorf("helius: get webhook response missing webhookURL, webhookType, or authHeader")
	}
	if len(current.TransactionTypes) > 0 {
		putBody.TransactionTypes = append([]string(nil), current.TransactionTypes...)
	}

	raw, err := json.Marshal(putBody)
	if err != nil {
		return fmt.Errorf("helius: marshal sync request: %w", err)
	}

	putStatus, putBodyBytes, err := exchange(ctx, h.client, httpclient.MethodPut, h.endpointURL("/v0/webhooks/"+url.PathEscape(id), key), heliusJSONHeaders(), raw)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("helius: build put webhook request: %w", err)
		}
		return fmt.Errorf("helius: put webhook call: %w", err)
	}
	if putStatus != httpclient.StatusOK {
		return fmt.Errorf("helius put webhook: status %d: %s", putStatus, putBodyBytes)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

func (h *HeliusProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	key, err := h.apiKeyFor(ctx)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("helius: webhookID is required")
	}

	status, respBody, err := exchange(ctx, h.client, httpclient.MethodDelete, h.endpointURL("/v0/webhooks/"+url.PathEscape(id), key), nil, nil)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("helius: build delete request: %w", err)
		}
		return fmt.Errorf("helius: delete webhook call: %w", err)
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return fmt.Errorf("helius delete webhook: status %d: %s", status, respBody)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — Authorization header matches stored authHeader (constant time)
// ---------------------------------------------------------------------------

func (h *HeliusProvider) VerifyInbound(headers Header, body []byte, secret string) (bool, error) {
	_ = body
	if err := gateInboundKey(context.Background(), h.keyAtUse); err != nil {
		return false, err
	}
	if err := rejectBlankSigningKey(secret); err != nil {
		return false, err
	}
	got := headers.Get("Authorization")
	if got == "" {
		return false, fmt.Errorf("helius: missing Authorization header")
	}
	want := secret
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return false, nil
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// ParsePayload — enhanced Solana transactions (JSON array)
// ---------------------------------------------------------------------------

type heliusEnhancedTx struct {
	Signature       string                 `json:"signature"`
	Slot            uint64                 `json:"slot"`
	Timestamp       *int64                 `json:"timestamp"`
	NativeTransfers []heliusNativeTransfer `json:"nativeTransfers"`
	TokenTransfers  []heliusTokenTransfer  `json:"tokenTransfers"`
}

type heliusNativeTransfer struct {
	FromUserAccount string `json:"fromUserAccount"`
	ToUserAccount   string `json:"toUserAccount"`
	Amount          int64  `json:"amount"`
}

type heliusTokenTransfer struct {
	FromUserAccount string          `json:"fromUserAccount"`
	ToUserAccount   string          `json:"toUserAccount"`
	TokenAmount     decimal.Decimal `json:"tokenAmount"`
	Mint            string          `json:"mint"`
	Decimals        *uint8          `json:"decimals,omitempty"`
}

func (h *HeliusProvider) ParsePayload(body []byte) ([]InboundTransfer, error) {
	var txs []heliusEnhancedTx
	if err := json.Unmarshal(body, &txs); err != nil {
		return nil, fmt.Errorf("helius: unmarshal payload: %w", err)
	}

	out := make([]InboundTransfer, 0)
	for _, tx := range txs {
		ts := time.Time{}
		if tx.Timestamp != nil && *tx.Timestamp > 0 {
			ts = time.Unix(*tx.Timestamp, 0).UTC()
		}

		for _, nt := range tx.NativeTransfers {
			if nt.ToUserAccount == "" && nt.FromUserAccount == "" {
				continue
			}
			out = append(out, InboundTransfer{
				TxHash:      tx.Signature,
				BlockNumber: tx.Slot,
				BlockHash:   "",
				From:        nt.FromUserAccount,
				To:          nt.ToUserAccount,
				Amount:      big.NewInt(nt.Amount),
				Asset:       models.ChainSOL,
				Token:       nil,
				LogIndex:    -1,
				Timestamp:   ts,
			})
		}

		for _, tt := range tx.TokenTransfers {
			if strings.TrimSpace(tt.Mint) == "" {
				continue
			}
			transfer := InboundTransfer{
				TxHash:      tx.Signature,
				BlockNumber: tx.Slot,
				BlockHash:   "",
				From:        tt.FromUserAccount,
				To:          tt.ToUserAccount,
				Asset:       "spl",
				Token: &types.Token{
					Contract: tt.Mint,
				},
				LogIndex:  -1,
				Timestamp: ts,
			}
			if tt.Decimals == nil {
				transfer.AmountIsHuman = true
				transfer.HumanAmount = tt.TokenAmount.String()
				transfer.Token.Decimals = 0
			} else {
				amount, err := humanToRawBigInt(tt.TokenAmount, *tt.Decimals)
				if err != nil {
					return nil, fmt.Errorf("helius: token amount (tx=%s mint=%s): %w", tx.Signature, tt.Mint, err)
				}
				transfer.Amount = amount
				transfer.Token.Decimals = *tt.Decimals
			}
			out = append(out, transfer)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *HeliusProvider) apiKeyFor(ctx context.Context) (string, error) {
	key, err := requireCredential(ctx, h.keyAtUse, h.apiKey)
	if err != nil {
		return "", fmt.Errorf("helius: API key is required")
	}
	return key, nil
}

func (h *HeliusProvider) endpointURL(path, key string) string {
	u := heliusAPIBase + path
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "api-key=" + url.QueryEscape(key)
}

func heliusJSONHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func heliusWebhookTypeForNetwork(network string) string {
	n := strings.ToLower(strings.TrimSpace(network))
	if strings.Contains(n, "devnet") {
		return heliusWebhookTypeDevnet
	}
	return heliusWebhookTypeMainnet
}

func resolveHeliusAuthHeader(cfgSecret string) (string, error) {
	s := strings.TrimSpace(cfgSecret)
	if s != "" {
		return s, nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("helius: generate auth header: %w", err)
	}
	return "Bearer " + hex.EncodeToString(b[:]), nil
}

func humanToRawBigInt(human decimal.Decimal, decimals uint8) (*big.Int, error) {
	if decimals > heliusMaxTokenDecimals {
		return nil, fmt.Errorf("decimals %d out of range", decimals)
	}
	if human.IsNegative() {
		return nil, fmt.Errorf("negative token amount")
	}
	return numeric.ToBaseUnits(human, int32(decimals))
}

var _ WebhookProvider = (*HeliusProvider)(nil)
