package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	heliusAPIBase     = "https://api-mainnet.helius-rpc.com"
	heliusHTTPTimeout = 30 * time.Second

	heliusWebhookTypeMainnet = "enhanced"
	heliusWebhookTypeDevnet  = "enhancedDevnet"

	defaultSPLDecimals = uint8(9)
)

type HeliusProvider struct {
	apiKey string
	client *http.Client
}

func NewHeliusProvider(apiKey string) *HeliusProvider {
	return &HeliusProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: heliusHTTPTimeout},
	}
}

func (h *HeliusProvider) ProviderName() string {
	return "helius"
}

// ---------------------------------------------------------------------------
// CreateWebhook
// ---------------------------------------------------------------------------

type heliusWebhookBody struct {
	WebhookURL         string   `json:"webhookURL"`
	WebhookType        string   `json:"webhookType"`
	AccountAddresses   []string `json:"accountAddresses"`
	TransactionTypes   []string `json:"transactionTypes"`
	AuthHeader         string   `json:"authHeader"`
}

type heliusCreateResp struct {
	WebhookID          string   `json:"webhookID"`
	WebhookURL         string   `json:"webhookURL"`
	WebhookType        string   `json:"webhookType"`
	AccountAddresses   []string `json:"accountAddresses"`
	TransactionTypes   []string `json:"transactionTypes"`
	AuthHeader         string   `json:"authHeader"`
	Active             bool     `json:"active"`
}

func (h *HeliusProvider) CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error) {
	if strings.TrimSpace(h.apiKey) == "" {
		return nil, fmt.Errorf("helius: API key is required")
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpointURL("/v0/webhooks"), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("helius: build create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("helius: create webhook call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, h.readError("create webhook", resp)
	}

	var result heliusCreateResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("helius: decode create response: %w", err)
	}

	if strings.TrimSpace(result.WebhookID) == "" {
		return nil, fmt.Errorf("helius: create response missing webhookID")
	}

	return &ProviderWebhook{
		ProviderWebhookID: result.WebhookID,
		SigningSecret:      authHeader,
	}, nil
}

// ---------------------------------------------------------------------------
// SyncAddresses — GET current webhook, PUT full replacement
// ---------------------------------------------------------------------------

func (h *HeliusProvider) SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error {
	if strings.TrimSpace(h.apiKey) == "" {
		return fmt.Errorf("helius: API key is required")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("helius: webhookID is required")
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, h.endpointURL("/v0/webhooks/"+url.PathEscape(id)), nil)
	if err != nil {
		return fmt.Errorf("helius: build get webhook request: %w", err)
	}

	getResp, err := h.client.Do(getReq)
	if err != nil {
		return fmt.Errorf("helius: get webhook call: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		return h.readError("get webhook", getResp)
	}

	var current heliusCreateResp
	if err := json.NewDecoder(getResp.Body).Decode(&current); err != nil {
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

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, h.endpointURL("/v0/webhooks/"+url.PathEscape(id)), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("helius: build put webhook request: %w", err)
	}
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := h.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("helius: put webhook call: %w", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		return h.readError("put webhook", putResp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

func (h *HeliusProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	if strings.TrimSpace(h.apiKey) == "" {
		return fmt.Errorf("helius: API key is required")
	}
	id := strings.TrimSpace(webhookID)
	if id == "" {
		return fmt.Errorf("helius: webhookID is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.endpointURL("/v0/webhooks/"+url.PathEscape(id)), nil)
	if err != nil {
		return fmt.Errorf("helius: build delete request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("helius: delete webhook call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return h.readError("delete webhook", resp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — Authorization header matches stored authHeader (constant time)
// ---------------------------------------------------------------------------

func (h *HeliusProvider) VerifyInbound(headers http.Header, body []byte, secret string) (bool, error) {
	_ = body
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
	FromUserAccount string  `json:"fromUserAccount"`
	ToUserAccount   string  `json:"toUserAccount"`
	TokenAmount     float64 `json:"tokenAmount"`
	Mint            string  `json:"mint"`
	Decimals        *uint8  `json:"decimals,omitempty"`
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
				Asset:       "sol",
				Token:       nil,
				LogIndex:    -1,
				Timestamp:   ts,
			})
		}

		for _, tt := range tx.TokenTransfers {
			if strings.TrimSpace(tt.Mint) == "" {
				continue
			}
			dec := defaultSPLDecimals
			if tt.Decimals != nil {
				dec = *tt.Decimals
			}
			amount, err := floatHumanToRawBigInt(tt.TokenAmount, dec)
			if err != nil {
				return nil, fmt.Errorf("helius: token amount (tx=%s mint=%s): %w", tx.Signature, tt.Mint, err)
			}
			out = append(out, InboundTransfer{
				TxHash:      tx.Signature,
				BlockNumber: tx.Slot,
				BlockHash:   "",
				From:        tt.FromUserAccount,
				To:          tt.ToUserAccount,
				Amount:      amount,
				Asset:       "spl",
				Token: &types.Token{
					Contract: tt.Mint,
					Decimals: dec,
				},
				LogIndex:  -1,
				Timestamp: ts,
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *HeliusProvider) endpointURL(path string) string {
	u := heliusAPIBase + path
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "api-key=" + url.QueryEscape(h.apiKey)
}

func (h *HeliusProvider) readError(label string, resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("helius %s: status %d: %s", label, resp.StatusCode, string(b))
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

func floatHumanToRawBigInt(human float64, decimals uint8) (*big.Int, error) {
	if decimals > 18 {
		return nil, fmt.Errorf("decimals %d out of range", decimals)
	}
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	val := new(big.Float).SetFloat64(human)
	if val == nil {
		return nil, fmt.Errorf("invalid token amount")
	}
	val.Mul(val, scale)
	raw, _ := val.Int(nil)
	if raw.Sign() < 0 {
		return nil, fmt.Errorf("negative token amount")
	}
	return raw, nil
}

var _ WebhookProvider = (*HeliusProvider)(nil)
