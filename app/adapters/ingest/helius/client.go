package helius

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/solanacredit"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	heliusAPIBase     = "https://api-mainnet.helius-rpc.com"
	heliusHTTPTimeout = 30 * time.Second

	heliusWebhookTypeMainnet = "enhanced"
	heliusWebhookTypeDevnet  = "enhancedDevnet"
)

type HeliusProvider struct {
	apiKey   string
	keyAtUse providers.KeySource
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
func (h *HeliusProvider) UseKeySource(source providers.KeySource) *HeliusProvider {
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

func (h *HeliusProvider) CreateWebhook(ctx context.Context, cfg providers.ProviderConfig) (*providers.ProviderWebhook, error) {
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
		return nil, chain.Unavailable(fmt.Errorf("helius: create webhook call: %w", err))
	}
	// Helius answers a created webhook with 201 (observed on mainnet, 2026-10-28).
	if status != httpclient.StatusOK && status != httpclient.StatusCreated {
		return nil, chain.FromProviderHTTP(status, string(respBody))
	}

	var result heliusCreateResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("helius: decode create response: %w", err)
	}

	if strings.TrimSpace(result.WebhookID) == "" {
		return nil, fmt.Errorf("helius: create response missing webhookID")
	}

	return &providers.ProviderWebhook{
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
		return chain.Unavailable(fmt.Errorf("helius: get webhook call: %w", err))
	}
	if getStatus != httpclient.StatusOK {
		return chain.FromProviderHTTP(getStatus, string(getBody))
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
		return chain.Unavailable(fmt.Errorf("helius: put webhook call: %w", err))
	}
	if putStatus != httpclient.StatusOK {
		return chain.FromProviderHTTP(putStatus, string(putBodyBytes))
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
		return chain.Unavailable(fmt.Errorf("helius: delete webhook call: %w", err))
	}
	if status != httpclient.StatusOK && status != httpclient.StatusNoContent {
		return chain.FromProviderHTTP(status, string(respBody))
	}
	return nil
}

// ---------------------------------------------------------------------------
// VerifyInbound — Authorization header matches stored authHeader (constant time)
// ---------------------------------------------------------------------------

func (h *HeliusProvider) VerifyInbound(headers providers.Header, body []byte, secret string) (bool, error) {
	_ = body
	if err := providers.GateInboundCredential(context.Background(), h.keyAtUse); err != nil {
		return false, err
	}
	if err := providers.RejectBlankSigningSecret(secret); err != nil {
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

// heliusEnhancedTx is the part of an enhanced transaction the deposit rule reads.
// accountData lists every account key of the transaction, in key order, with its
// lamport change and the changes of the token balances it holds: the same balances
// the deposit scanner reads from getBlock, so the credits and their positions are the
// scanner's (solanacredit.Lamports, solanacredit.Tokens). nativeTransfers
// and tokenTransfers (one per instruction, not netted) are not read.
type heliusEnhancedTx struct {
	Signature        string              `json:"signature"`
	Slot             uint64              `json:"slot"`
	Timestamp        *int64              `json:"timestamp"`
	TransactionError json.RawMessage     `json:"transactionError"`
	AccountData      []heliusAccountData `json:"accountData"`
}

type heliusAccountData struct {
	Account             string                     `json:"account"`
	NativeBalanceChange json.Number                `json:"nativeBalanceChange"`
	TokenBalanceChanges []heliusTokenBalanceChange `json:"tokenBalanceChanges"`
}

// heliusTokenBalanceChange is the signed change of one token account, in base units.
type heliusTokenBalanceChange struct {
	UserAccount    string `json:"userAccount"`
	TokenAccount   string `json:"tokenAccount"`
	Mint           string `json:"mint"`
	RawTokenAmount struct {
		TokenAmount string `json:"tokenAmount"`
	} `json:"rawTokenAmount"`
}

func (h *HeliusProvider) ParsePayload(body []byte) ([]providers.InboundTransfer, error) {
	var txs []heliusEnhancedTx
	if err := json.Unmarshal(body, &txs); err != nil {
		return nil, fmt.Errorf("helius: unmarshal payload: %w", err)
	}

	out := make([]providers.InboundTransfer, 0)
	for _, tx := range txs {
		if failed(tx.TransactionError) {
			continue
		}
		credits, err := enhancedTxCredits(tx)
		if err != nil {
			// Left to the deposit scanner: recording it without its positions could
			// record it twice. The other transactions of the push are kept.
			slog.Warn("helius: transaction skipped, its credits cannot be placed", "tx", tx.Signature, "error", err)
			continue
		}
		out = append(out, credits...)
	}
	return out, nil
}

// failed reports a transactionError other than null: a failed transaction only
// moves its fee.
func failed(transactionError json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(transactionError))
	return trimmed != "" && trimmed != "null"
}

// enhancedTxCredits returns the lamport credits, in account order, then the token
// credits of one successful transaction. A transaction whose balances cannot be read
// or placed among its accounts is an error: a credit without its position could be
// recorded twice.
func enhancedTxCredits(tx heliusEnhancedTx) ([]providers.InboundTransfer, error) {
	if len(tx.AccountData) == 0 {
		return nil, fmt.Errorf("no accountData to read its balances from")
	}
	ts := time.Time{}
	if tx.Timestamp != nil && *tx.Timestamp > 0 {
		ts = time.Unix(*tx.Timestamp, 0).UTC()
	}
	accounts := make([]string, len(tx.AccountData))
	lamports := make([]*big.Int, len(tx.AccountData))
	index := make(map[string]int, len(tx.AccountData))
	for i, entry := range tx.AccountData {
		change, ok := new(big.Int), true
		if entry.NativeBalanceChange != "" {
			_, ok = change.SetString(entry.NativeBalanceChange.String(), 10)
		}
		if !ok {
			return nil, fmt.Errorf("account %d: nativeBalanceChange %q is not an integer", i, entry.NativeBalanceChange)
		}
		accounts[i], lamports[i] = entry.Account, change
		if _, seen := index[entry.Account]; !seen {
			index[entry.Account] = i
		}
	}
	var changes []solanacredit.TokenChange
	seen := make(map[string]bool)
	for _, entry := range tx.AccountData {
		for _, token := range entry.TokenBalanceChanges {
			position, listed := index[token.TokenAccount]
			if !listed {
				return nil, fmt.Errorf("token account %s is not one of its accounts", token.TokenAccount)
			}
			key := token.TokenAccount + "/" + token.Mint
			if seen[key] {
				return nil, fmt.Errorf("token account %s is listed twice", token.TokenAccount)
			}
			seen[key] = true
			if token.UserAccount == "" || strings.TrimSpace(token.Mint) == "" {
				return nil, fmt.Errorf("token account %s has no owner or mint", token.TokenAccount)
			}
			delta, ok := new(big.Int).SetString(token.RawTokenAmount.TokenAmount, 10)
			if !ok {
				return nil, fmt.Errorf("token account %s: amount %q is not a base-unit integer", token.TokenAccount, token.RawTokenAmount.TokenAmount)
			}
			changes = append(changes, solanacredit.TokenChange{Index: position, Owner: token.UserAccount, Mint: token.Mint, Delta: delta})
		}
	}

	feePayer := accounts[0]
	transfer := func(from, to string, amount *big.Int, asset string, token *types.Token, position int) providers.InboundTransfer {
		return providers.InboundTransfer{
			TxHash: tx.Signature, BlockNumber: tx.Slot, From: from, To: to, Amount: amount,
			Asset: asset, Token: token, LogIndex: position, Timestamp: ts,
		}
	}
	var out []providers.InboundTransfer
	for _, c := range solanacredit.Lamports(accounts, lamports) {
		out = append(out, transfer(feePayer, c.Account, c.Amount, models.ChainSOL, nil, c.Index))
	}
	for _, c := range solanacredit.Tokens(changes) {
		from := c.From
		if from == "" {
			from = feePayer
		}
		// The token row (symbol, decimals) comes from the registry in the ingest service;
		// unregistered mints are left there.
		out = append(out, transfer(from, c.Owner, c.Amount, "spl", &types.Token{Contract: c.Mint}, solanacredit.TokenPosition(c.Index)))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *HeliusProvider) apiKeyFor(ctx context.Context) (string, error) {
	key, err := providers.CredentialForCall(ctx, h.keyAtUse, h.apiKey)
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
var _ providers.WebhookProvider = (*HeliusProvider)(nil)
