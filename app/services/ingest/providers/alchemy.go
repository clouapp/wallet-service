package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	alchemyAPIBase       = "https://dashboard.alchemy.com/api"
	alchemySignatureHdr  = "X-Alchemy-Signature"
	alchemyAuthTokenHdr  = "X-Alchemy-Token"
	alchemyAddrPageLimit = 100
	alchemyHTTPTimeout   = 30 * time.Second
)

type AlchemyProvider struct {
	apiKey string
	client *http.Client
}

func NewAlchemyProvider(apiKey string) *AlchemyProvider {
	return &AlchemyProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: alchemyHTTPTimeout},
	}
}

func (a *AlchemyProvider) ProviderName() string {
	return "alchemy"
}

// ---------------------------------------------------------------------------
// CreateWebhook
// ---------------------------------------------------------------------------

type alchemyCreateReq struct {
	Network     string   `json:"network"`
	WebhookType string   `json:"webhook_type"`
	WebhookURL  string   `json:"webhook_url"`
	Addresses   []string `json:"addresses"`
}

type alchemyCreateResp struct {
	Data struct {
		ID         string `json:"id"`
		SigningKey string `json:"signing_key"`
	} `json:"data"`
}

func (a *AlchemyProvider) CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error) {
	payload := alchemyCreateReq{
		Network:     cfg.Network,
		WebhookType: "ADDRESS_ACTIVITY",
		WebhookURL:  cfg.WebhookURL,
		Addresses:   cfg.Addresses,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("alchemy: marshal create request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, alchemyAPIBase+"/create-webhook", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("alchemy: build create request: %w", err)
	}
	a.setHeaders(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alchemy: create webhook call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, a.readError("create-webhook", resp)
	}

	var result alchemyCreateResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("alchemy: decode create response: %w", err)
	}

	return &ProviderWebhook{
		ProviderWebhookID: result.Data.ID,
		SigningSecret:      result.Data.SigningKey,
	}, nil
}

// ---------------------------------------------------------------------------
// SyncAddresses — fetch current, diff, patch
// ---------------------------------------------------------------------------

type alchemyAddrPage struct {
	Data       []string `json:"data"`
	Pagination struct {
		Cursors struct {
			After string `json:"after"`
		} `json:"cursors"`
	} `json:"pagination"`
}

type alchemyPatchAddressesReq struct {
	WebhookID         string   `json:"webhook_id"`
	AddressesToAdd    []string `json:"addresses_to_add"`
	AddressesToRemove []string `json:"addresses_to_remove"`
}

func (a *AlchemyProvider) SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error {
	current, err := a.fetchAllAddresses(ctx, webhookID)
	if err != nil {
		return fmt.Errorf("alchemy: fetch current addresses: %w", err)
	}

	toAdd, toRemove := diffAddresses(current, allAddresses)
	if len(toAdd) == 0 && len(toRemove) == 0 {
		return nil
	}

	payload := alchemyPatchAddressesReq{
		WebhookID:         webhookID,
		AddressesToAdd:    toAdd,
		AddressesToRemove: toRemove,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("alchemy: marshal patch request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, alchemyAPIBase+"/update-webhook-addresses", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alchemy: build patch request: %w", err)
	}
	a.setHeaders(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("alchemy: patch addresses call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return a.readError("update-webhook-addresses", resp)
	}
	return nil
}

func (a *AlchemyProvider) fetchAllAddresses(ctx context.Context, webhookID string) ([]string, error) {
	var all []string
	cursor := ""

	for {
		u := fmt.Sprintf("%s/webhook-addresses?webhook_id=%s&limit=%d", alchemyAPIBase, webhookID, alchemyAddrPageLimit)
		if cursor != "" {
			u += "&after=" + cursor
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		a.setHeaders(req)

		resp, err := a.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			respErr := a.readError("webhook-addresses", resp)
			resp.Body.Close()
			return nil, respErr
		}

		var page alchemyAddrPage
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		all = append(all, page.Data...)

		if page.Pagination.Cursors.After == "" {
			break
		}
		cursor = page.Pagination.Cursors.After
	}

	return all, nil
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

type alchemyDeleteReq struct {
	WebhookID string `json:"webhook_id"`
}

func (a *AlchemyProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	body, err := json.Marshal(alchemyDeleteReq{WebhookID: webhookID})
	if err != nil {
		return fmt.Errorf("alchemy: marshal delete request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, alchemyAPIBase+"/delete-webhook", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alchemy: build delete request: %w", err)
	}
	a.setHeaders(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("alchemy: delete webhook call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return a.readError("delete-webhook", resp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — HMAC-SHA256 signature verification
// ---------------------------------------------------------------------------

func (a *AlchemyProvider) VerifyInbound(headers http.Header, body []byte, secret string) (bool, error) {
	sig := headers.Get(alchemySignatureHdr)
	if sig == "" {
		return false, fmt.Errorf("alchemy: missing %s header", alchemySignatureHdr)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}

// ---------------------------------------------------------------------------
// ParsePayload — ADDRESS_ACTIVITY event
// ---------------------------------------------------------------------------

type alchemyEvent struct {
	Event struct {
		Activity []alchemyActivity `json:"activity"`
	} `json:"event"`
}

type alchemyActivity struct {
	BlockNum    string  `json:"blockNum"`
	Hash        string  `json:"hash"`
	FromAddress string  `json:"fromAddress"`
	ToAddress   string  `json:"toAddress"`
	Value       float64 `json:"value"`
	Asset       string  `json:"asset"`
	Category    string  `json:"category"`
	RawContract struct {
		RawValue string `json:"rawValue"`
		Address  string `json:"address"`
		Decimals int    `json:"decimals"`
	} `json:"rawContract"`
	Log struct {
		LogIndex  string `json:"logIndex"`
		BlockHash string `json:"blockHash"`
	} `json:"log"`
}

func (a *AlchemyProvider) ParsePayload(body []byte) ([]InboundTransfer, error) {
	var event alchemyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("alchemy: unmarshal payload: %w", err)
	}

	transfers := make([]InboundTransfer, 0, len(event.Event.Activity))
	for _, act := range event.Event.Activity {
		t, err := activityToTransfer(act)
		if err != nil {
			return nil, fmt.Errorf("alchemy: parse activity (tx=%s): %w", act.Hash, err)
		}
		transfers = append(transfers, t)
	}
	return transfers, nil
}

func activityToTransfer(act alchemyActivity) (InboundTransfer, error) {
	blockNum, err := parseHexUint64(act.BlockNum)
	if err != nil {
		return InboundTransfer{}, fmt.Errorf("parse blockNum %q: %w", act.BlockNum, err)
	}

	amount := parseAmount(act)

	logIndex := -1
	if act.Log.LogIndex != "" {
		parsed, err := parseHexInt(act.Log.LogIndex)
		if err != nil {
			return InboundTransfer{}, fmt.Errorf("parse logIndex %q: %w", act.Log.LogIndex, err)
		}
		logIndex = parsed
	}

	t := InboundTransfer{
		TxHash:      act.Hash,
		BlockNumber: blockNum,
		BlockHash:   act.Log.BlockHash,
		From:        act.FromAddress,
		To:          act.ToAddress,
		Amount:      amount,
		Asset:       act.Asset,
		LogIndex:    logIndex,
	}

	if act.Category == "token" && act.RawContract.Address != "" {
		t.Token = &types.Token{
			Contract: act.RawContract.Address,
			Decimals: uint8(act.RawContract.Decimals),
		}
	}

	return t, nil
}

func parseAmount(act alchemyActivity) *big.Int {
	if act.RawContract.RawValue != "" {
		raw := strings.TrimPrefix(act.RawContract.RawValue, "0x")
		if val, ok := new(big.Int).SetString(raw, 16); ok {
			return val
		}
	}

	// Fallback: convert the float value to wei (18 decimals for native ETH).
	if act.Value != 0 {
		weiPerEth := new(big.Float).SetFloat64(math.Pow10(18))
		val := new(big.Float).SetFloat64(act.Value)
		val.Mul(val, weiPerEth)
		wei, _ := val.Int(nil)
		return wei
	}

	return big.NewInt(0)
}

func parseHexUint64(s string) (uint64, error) {
	s = strings.TrimPrefix(s, "0x")
	return strconv.ParseUint(s, 16, 64)
}

func parseHexInt(s string) (int, error) {
	s = strings.TrimPrefix(s, "0x")
	v, err := strconv.ParseInt(s, 16, 64)
	return int(v), err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *AlchemyProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(alchemyAuthTokenHdr, a.apiKey)
}

func (a *AlchemyProvider) readError(endpoint string, resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("alchemy %s: status %d: %s", endpoint, resp.StatusCode, string(body))
}

func diffAddresses(current, desired []string) (toAdd, toRemove []string) {
	currentSet := make(map[string]struct{}, len(current))
	for _, addr := range current {
		currentSet[strings.ToLower(addr)] = struct{}{}
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, addr := range desired {
		desiredSet[strings.ToLower(addr)] = struct{}{}
	}

	for _, addr := range desired {
		if _, exists := currentSet[strings.ToLower(addr)]; !exists {
			toAdd = append(toAdd, addr)
		}
	}

	for _, addr := range current {
		if _, exists := desiredSet[strings.ToLower(addr)]; !exists {
			toRemove = append(toRemove, addr)
		}
	}

	return toAdd, toRemove
}

// compile-time interface check
var _ WebhookProvider = (*AlchemyProvider)(nil)
