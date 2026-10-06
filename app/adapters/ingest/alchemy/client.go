package alchemy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/shopspring/decimal"
)

const (
	alchemyAPIBase       = "https://dashboard.alchemy.com/api"
	alchemySignatureHdr  = "X-Alchemy-Signature"
	alchemyAuthTokenHdr  = "X-Alchemy-Token"
	alchemyAddrPageLimit = 100
	alchemyHTTPTimeout   = 30 * time.Second
	// alchemyNativeDecimals converts a native ETH value to wei.
	alchemyNativeDecimals = 18
)

type AlchemyProvider struct {
	apiKey   string
	keyAtUse providers.KeySource
	client   *httpclient.Client
}

func NewAlchemyProvider(apiKey string) *AlchemyProvider {
	return &AlchemyProvider{
		apiKey: apiKey,
		client: httpclient.NewClient(alchemyHTTPTimeout),
	}
}

// UseKeySource reads the credential on each call. The constructor key is
// not the one used after this is set, so boot does not capture it.
func (a *AlchemyProvider) UseKeySource(source providers.KeySource) *AlchemyProvider {
	if a == nil {
		return nil
	}
	a.keyAtUse = source
	return a
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

func (a *AlchemyProvider) CreateWebhook(ctx context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error) {
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

	headers, headerErr := a.apiHeaders(ctx)
	if headerErr != nil {
		return nil, headerErr
	}

	status, respBody, err := exchange(ctx, a.client, httpclient.MethodPost, alchemyAPIBase+"/create-webhook", headers, body)
	if err != nil {
		if httpclient.IsBuild(err) {
			return nil, fmt.Errorf("alchemy: build create request: %w", err)
		}
		return nil, chain.Unavailable(fmt.Errorf("alchemy: create webhook call: %w", err))
	}
	if status != httpclient.StatusOK {
		return nil, chain.FromProviderHTTP(status, string(respBody))
	}

	var result alchemyCreateResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("alchemy: decode create response: %w", err)
	}

	return &providers.ProviderWebhook{
		ProviderWebhookID: result.Data.ID,
		SigningSecret:     result.Data.SigningKey,
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

	headers, headerErr := a.apiHeaders(ctx)
	if headerErr != nil {
		return headerErr
	}

	status, respBody, err := exchange(ctx, a.client, httpclient.MethodPatch, alchemyAPIBase+"/update-webhook-addresses", headers, body)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("alchemy: build patch request: %w", err)
		}
		return chain.Unavailable(fmt.Errorf("alchemy: patch addresses call: %w", err))
	}
	if status != httpclient.StatusOK {
		return chain.FromProviderHTTP(status, string(respBody))
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

		headers, headerErr := a.apiHeaders(ctx)
		if headerErr != nil {
			return nil, headerErr
		}

		status, respBody, err := exchange(ctx, a.client, httpclient.MethodGet, u, headers, nil)
		if err != nil {
			return nil, err
		}
		if status != httpclient.StatusOK {
			return nil, chain.FromProviderHTTP(status, string(respBody))
		}

		var page alchemyAddrPage
		if err := json.Unmarshal(respBody, &page); err != nil {
			return nil, err
		}

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

	headers, headerErr := a.apiHeaders(ctx)
	if headerErr != nil {
		return headerErr
	}

	status, respBody, err := exchange(ctx, a.client, httpclient.MethodDelete, alchemyAPIBase+"/delete-webhook", headers, body)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("alchemy: build delete request: %w", err)
		}
		return chain.Unavailable(fmt.Errorf("alchemy: delete webhook call: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return chain.FromProviderHTTP(status, string(respBody))
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — HMAC-SHA256 signature verification
// ---------------------------------------------------------------------------

func (a *AlchemyProvider) VerifyInbound(headers providers.Header, body []byte, secret string) (bool, error) {
	if err := providers.GateInboundCredential(context.Background(), a.keyAtUse); err != nil {
		return false, err
	}
	if err := providers.RejectBlankSigningSecret(secret); err != nil {
		return false, err
	}

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
	BlockNum    string          `json:"blockNum"`
	Hash        string          `json:"hash"`
	FromAddress string          `json:"fromAddress"`
	ToAddress   string          `json:"toAddress"`
	Value       decimal.Decimal `json:"value"`
	Asset       string          `json:"asset"`
	Category    string          `json:"category"`
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

func (a *AlchemyProvider) ParsePayload(body []byte) ([]providers.InboundTransfer, error) {
	var event alchemyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("alchemy: unmarshal payload: %w", err)
	}

	transfers := make([]providers.InboundTransfer, 0, len(event.Event.Activity))
	for _, act := range event.Event.Activity {
		t, err := activityToTransfer(act)
		if err != nil {
			return nil, fmt.Errorf("alchemy: parse activity (tx=%s): %w", act.Hash, err)
		}
		transfers = append(transfers, t)
	}
	return transfers, nil
}

func activityToTransfer(act alchemyActivity) (providers.InboundTransfer, error) {
	blockNum, err := parseHexUint64(act.BlockNum)
	if err != nil {
		return providers.InboundTransfer{}, fmt.Errorf("parse blockNum %q: %w", act.BlockNum, err)
	}

	amountIsHuman := false
	humanAmount := ""
	var amount *big.Int
	if act.Category == "token" && act.RawContract.RawValue == "" {
		amountIsHuman = true
		humanAmount = act.Value.String()
	} else {
		amount, err = parseAmount(act)
		if err != nil {
			return providers.InboundTransfer{}, err
		}
	}

	logIndex := -1
	if act.Log.LogIndex != "" {
		parsed, err := parseHexInt(act.Log.LogIndex)
		if err != nil {
			return providers.InboundTransfer{}, fmt.Errorf("parse logIndex %q: %w", act.Log.LogIndex, err)
		}
		logIndex = parsed
	}

	t := providers.InboundTransfer{
		TxHash:        act.Hash,
		BlockNumber:   blockNum,
		BlockHash:     act.Log.BlockHash,
		From:          act.FromAddress,
		To:            act.ToAddress,
		Amount:        amount,
		AmountIsHuman: amountIsHuman,
		HumanAmount:   humanAmount,
		Asset:         act.Asset,
		LogIndex:      logIndex,
	}

	if act.Category == "token" && act.RawContract.Address != "" {
		t.Token = &types.Token{
			Contract: act.RawContract.Address,
			Decimals: uint8(act.RawContract.Decimals),
		}
	}

	return t, nil
}

// parseAmount prefers the exact raw value; otherwise it converts the decimal value
// to wei (native ETH has alchemyNativeDecimals).
func parseAmount(act alchemyActivity) (*big.Int, error) {
	if act.RawContract.RawValue != "" {
		raw := strings.TrimPrefix(act.RawContract.RawValue, "0x")
		if val, ok := new(big.Int).SetString(raw, 16); ok {
			return val, nil
		}
	}

	wei, err := numeric.ToBaseUnits(act.Value, alchemyNativeDecimals)
	if err != nil {
		return nil, fmt.Errorf("value %s: %w", act.Value.String(), err)
	}
	return wei, nil
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

func (a *AlchemyProvider) apiHeaders(ctx context.Context) (map[string]string, error) {
	key, err := providers.CredentialForCall(ctx, a.keyAtUse, a.apiKey)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"Content-Type":      "application/json",
		alchemyAuthTokenHdr: key,
	}, nil
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
var _ providers.WebhookProvider = (*AlchemyProvider)(nil)
