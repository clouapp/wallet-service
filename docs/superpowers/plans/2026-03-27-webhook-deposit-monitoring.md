# Webhook-Based Deposit Monitoring — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace RPC-polling block scanning with push-based deposit detection via Alchemy (EVM), Helius (Solana), and QuickNode (Bitcoin) webhooks, using free block explorer APIs for confirmation tracking.

**Architecture:** Provider webhook adapters behind a common `WebhookProvider` interface receive inbound transaction notifications at `/v1/webhooks/ingest/:provider/:chain`. A `WebhookSyncService` keeps provider address lists in sync on wallet create/deactivate. A lightweight confirmation tracker Lambda fetches block heights from free APIs (Etherscan, Blockstream, Solana public RPC) to advance deposit confirmations.

**Tech Stack:** Go (Goravel), PostgreSQL, Redis, AWS Lambda + EventBridge + SQS, Alchemy Notify API, Helius Webhook API, QuickNode Streams REST API

**Spec:** `docs/superpowers/specs/2026-03-27-webhook-deposit-monitoring-design.md`

---

## Phase 1: Database Migrations & Models

### Task 1: Create `webhook_subscriptions` table

**Files:**
- Create: `back/database/migrations/20260327100001_create_webhook_subscriptions_table.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write the migration file**

Create `back/database/migrations/20260327100001_create_webhook_subscriptions_table.go`:

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327100001CreateWebhookSubscriptionsTable struct{}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Signature() string {
	return "20260327100001_create_webhook_subscriptions_table"
}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Up() error {
	return facades.Schema().Create("webhook_subscriptions", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain_id", 20)
		table.String("provider", 20)
		table.String("provider_webhook_id", 255)
		table.Text("webhook_url")
		table.Text("signing_secret")
		table.String("status", 20).Default("active")
		table.String("sync_status", 20).Default("synced")
		table.String("synced_addresses_hash", 64).Nullable()
		table.Timestamp("last_synced_at").Nullable()
		table.Timestamps()

		table.Foreign("chain_id").References("id").On("chains")
		table.Index("idx_ws_chain_provider", "chain_id", "provider")
	})
}

func (r *M20260327100001CreateWebhookSubscriptionsTable) Down() error {
	return facades.Schema().DropIfExists("webhook_subscriptions")
}
```

- [ ] **Step 2: Register migration in migrations.go**

Add `&M20260327100001CreateWebhookSubscriptionsTable{},` to the `All()` return slice in `back/database/migrations/migrations.go`.

- [ ] **Step 3: Run migration**

```bash
cd back && go run . artisan migrate
```

Expected: migration runs without error.

- [ ] **Step 4: Commit**

```bash
git add back/database/migrations/20260327100001_create_webhook_subscriptions_table.go back/database/migrations/migrations.go
git commit -m "feat: add webhook_subscriptions table migration"
```

---

### Task 2: Add `log_index` to `transactions` table

**Files:**
- Create: `back/database/migrations/20260327100002_add_log_index_to_transactions.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write the migration file**

Create `back/database/migrations/20260327100002_add_log_index_to_transactions.go`:

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260327100002AddLogIndexToTransactions struct{}

func (r *M20260327100002AddLogIndexToTransactions) Signature() string {
	return "20260327100002_add_log_index_to_transactions"
}

func (r *M20260327100002AddLogIndexToTransactions) Up() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.Integer("log_index").Default(-1)
		table.Index("idx_tx_dedup", "chain", "tx_hash", "log_index", "tx_type")
	})
}

func (r *M20260327100002AddLogIndexToTransactions) Down() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.DropIndex("idx_tx_dedup")
		table.DropColumn("log_index")
	})
}
```

- [ ] **Step 2: Register migration and run**

Add to `All()` in `migrations.go`, then `go run . artisan migrate`.

- [ ] **Step 3: Commit**

```bash
git add back/database/migrations/20260327100002_add_log_index_to_transactions.go back/database/migrations/migrations.go
git commit -m "feat: add log_index column to transactions for multi-event dedup"
```

---

### Task 3: WebhookSubscription model

**Files:**
- Create: `back/app/models/webhook_subscription.go`

- [ ] **Step 1: Write the model**

Create `back/app/models/webhook_subscription.go`:

```go
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WebhookSubscription struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	ChainID             string     `gorm:"type:varchar(20);not null" json:"chain_id"`
	Provider            string     `gorm:"type:varchar(20);not null" json:"provider"`
	ProviderWebhookID   string     `gorm:"type:varchar(255);not null" json:"provider_webhook_id"`
	WebhookURL          string     `gorm:"type:text;not null" json:"webhook_url"`
	SigningSecret        string     `gorm:"type:text;not null" json:"-"`
	Status              string     `gorm:"type:varchar(20);default:active" json:"status"`
	SyncStatus          string     `gorm:"type:varchar(20);default:synced" json:"sync_status"`
	SyncedAddressesHash *string    `gorm:"type:varchar(64)" json:"-"`
	LastSyncedAt        *time.Time `gorm:"type:timestamptz" json:"last_synced_at,omitempty"`
}

func (w *WebhookSubscription) TableName() string { return "webhook_subscriptions" }
```

- [ ] **Step 2: Commit**

```bash
git add back/app/models/webhook_subscription.go
git commit -m "feat: add WebhookSubscription model"
```

---

### Task 4: WebhookSubscription repository

**Files:**
- Create: `back/app/repositories/webhook_subscription_repository.go`

- [ ] **Step 1: Write the repository**

Create `back/app/repositories/webhook_subscription_repository.go`:

```go
package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WebhookSubscriptionRepository interface {
	FindByChainID(chainID string) (*models.WebhookSubscription, error)
	FindByProviderAndChain(provider, chainID string) (*models.WebhookSubscription, error)
	FindAllActive() ([]models.WebhookSubscription, error)
	Create(sub *models.WebhookSubscription) error
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
}

type webhookSubscriptionRepository struct{}

func NewWebhookSubscriptionRepository() WebhookSubscriptionRepository {
	return &webhookSubscriptionRepository{}
}

func (r *webhookSubscriptionRepository) FindByChainID(chainID string) (*models.WebhookSubscription, error) {
	var sub models.WebhookSubscription
	err := facades.Orm().Query().Where("chain_id = ? AND status = ?", chainID, "active").First(&sub)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *webhookSubscriptionRepository) FindByProviderAndChain(provider, chainID string) (*models.WebhookSubscription, error) {
	var sub models.WebhookSubscription
	err := facades.Orm().Query().Where("provider = ? AND chain_id = ?", provider, chainID).First(&sub)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *webhookSubscriptionRepository) FindAllActive() ([]models.WebhookSubscription, error) {
	var subs []models.WebhookSubscription
	err := facades.Orm().Query().Where("status = ?", "active").Find(&subs)
	return subs, err
}

func (r *webhookSubscriptionRepository) Create(sub *models.WebhookSubscription) error {
	return facades.Orm().Query().Create(sub)
}

func (r *webhookSubscriptionRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	return facades.Orm().Query().Model(&models.WebhookSubscription{}).Where("id = ?", id).Updates(fields)
}
```

- [ ] **Step 2: Commit**

```bash
git add back/app/repositories/webhook_subscription_repository.go
git commit -m "feat: add WebhookSubscription repository"
```

---

### Task 5: Update TransactionRepository for log_index dedup

**Files:**
- Modify: `back/app/repositories/transaction_repository.go`
- Modify: `back/app/models/transaction.go`

- [ ] **Step 1: Add `LogIndex` field to Transaction model**

In `back/app/models/transaction.go`, add to the `Transaction` struct:

```go
LogIndex int `gorm:"type:int;default:-1" json:"log_index"`
```

- [ ] **Step 2: Add new dedup method to TransactionRepository**

Add to the interface and implementation in `back/app/repositories/transaction_repository.go`:

Interface method:
```go
CountByChainTxHashAndLogIndex(chainID, txHash string, logIndex int, txType string) (int64, error)
```

Implementation:
```go
func (r *transactionRepository) CountByChainTxHashAndLogIndex(chainID, txHash string, logIndex int, txType string) (int64, error) {
	var count int64
	err := facades.Orm().Query().Model(&models.Transaction{}).
		Where("chain = ? AND tx_hash = ? AND log_index = ? AND tx_type = ?", chainID, txHash, logIndex, txType).
		Count(&count)
	return count, err
}
```

- [ ] **Step 3: Run existing tests**

```bash
cd back && go test ./app/repositories/... -v -count=1
```

Expected: all existing tests pass.

- [ ] **Step 4: Commit**

```bash
git add back/app/models/transaction.go back/app/repositories/transaction_repository.go
git commit -m "feat: add log_index dedup to Transaction model and repository"
```

---

## Phase 2: Provider Interface & Implementations

### Task 6: WebhookProvider interface and types

**Files:**
- Create: `back/app/services/ingest/providers/provider.go`

- [ ] **Step 1: Write the interface and types**

Create `back/app/services/ingest/providers/provider.go`:

```go
package providers

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

type InboundTransfer struct {
	TxHash      string
	BlockNumber uint64
	BlockHash   string
	From        string
	To          string
	Amount      *big.Int
	Asset       string
	Token       *types.Token
	LogIndex    int
	Timestamp   time.Time
}

type ProviderConfig struct {
	ChainID    string
	Network    string
	WebhookURL string
	Addresses  []string
	APIKey     string
	AuthSecret string
}

type ProviderWebhook struct {
	ProviderWebhookID string
	SigningSecret      string
}

type WebhookProvider interface {
	ProviderName() string
	CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error)
	SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error
	DeleteWebhook(ctx context.Context, webhookID string) error
	VerifyInbound(headers http.Header, body []byte, secret string) (bool, error)
	ParsePayload(body []byte) ([]InboundTransfer, error)
}
```

- [ ] **Step 2: Commit**

```bash
git add back/app/services/ingest/providers/provider.go
git commit -m "feat: add WebhookProvider interface and shared types"
```

---

### Task 7: Alchemy provider

**Files:**
- Create: `back/app/services/ingest/providers/alchemy.go`
- Create: `back/app/services/ingest/providers/alchemy_test.go`

- [ ] **Step 1: Write the Alchemy provider**

Create `back/app/services/ingest/providers/alchemy.go`:

```go
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
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	alchemyBaseURL          = "https://dashboard.alchemy.com/api"
	alchemySignatureHeader  = "X-Alchemy-Signature"
	alchemyAuthTokenHeader  = "X-Alchemy-Token"
)

type AlchemyProvider struct {
	authToken  string
	httpClient *http.Client
}

func NewAlchemyProvider(authToken string) *AlchemyProvider {
	return &AlchemyProvider{
		authToken:  authToken,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AlchemyProvider) ProviderName() string { return "alchemy" }

func (a *AlchemyProvider) CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error) {
	payload := map[string]interface{}{
		"network":      cfg.Network,
		"webhook_type": "ADDRESS_ACTIVITY",
		"webhook_url":  cfg.WebhookURL,
		"addresses":    cfg.Addresses,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", alchemyBaseURL+"/create-webhook", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(alchemyAuthTokenHeader, a.authToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("alchemy create webhook: status %d, body: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Data struct {
			ID         string `json:"id"`
			SigningKey string `json:"signing_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("alchemy create webhook decode: %w", err)
	}

	return &ProviderWebhook{
		ProviderWebhookID: result.Data.ID,
		SigningSecret:      result.Data.SigningKey,
	}, nil
}

func (a *AlchemyProvider) SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error {
	currentAddrs, err := a.fetchCurrentAddresses(ctx, webhookID)
	if err != nil {
		return fmt.Errorf("alchemy fetch addresses: %w", err)
	}

	currentSet := make(map[string]bool, len(currentAddrs))
	for _, addr := range currentAddrs {
		currentSet[strings.ToLower(addr)] = true
	}

	desiredSet := make(map[string]bool, len(allAddresses))
	for _, addr := range allAddresses {
		desiredSet[strings.ToLower(addr)] = true
	}

	var toAdd, toRemove []string
	for addr := range desiredSet {
		if !currentSet[addr] {
			toAdd = append(toAdd, addr)
		}
	}
	for addr := range currentSet {
		if !desiredSet[addr] {
			toRemove = append(toRemove, addr)
		}
	}

	if len(toAdd) == 0 && len(toRemove) == 0 {
		return nil
	}

	payload := map[string]interface{}{
		"webhook_id":         webhookID,
		"addresses_to_add":   toAdd,
		"addresses_to_remove": toRemove,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "PATCH", alchemyBaseURL+"/update-webhook-addresses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(alchemyAuthTokenHeader, a.authToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("alchemy update addresses: status %d, body: %s", resp.StatusCode, respBody)
	}
	return nil
}

func (a *AlchemyProvider) fetchCurrentAddresses(ctx context.Context, webhookID string) ([]string, error) {
	var allAddrs []string
	cursor := ""

	for {
		url := fmt.Sprintf("%s/webhook-addresses?webhook_id=%s&limit=100", alchemyBaseURL, webhookID)
		if cursor != "" {
			url += "&after=" + cursor
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set(alchemyAuthTokenHeader, a.authToken)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var result struct {
			Data       []string `json:"data"`
			Pagination struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				TotalCount int `json:"total_count"`
			} `json:"pagination"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		allAddrs = append(allAddrs, result.Data...)
		if result.Pagination.Cursors.After == "" || len(result.Data) == 0 {
			break
		}
		cursor = result.Pagination.Cursors.After
	}

	return allAddrs, nil
}

func (a *AlchemyProvider) DeleteWebhook(ctx context.Context, webhookID string) error {
	payload := map[string]string{"webhook_id": webhookID}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "DELETE", alchemyBaseURL+"/delete-webhook", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(alchemyAuthTokenHeader, a.authToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (a *AlchemyProvider) VerifyInbound(headers http.Header, body []byte, secret string) (bool, error) {
	signature := headers.Get(alchemySignatureHeader)
	if signature == "" {
		return false, fmt.Errorf("missing %s header", alchemySignatureHeader)
	}

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	expected := hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature)), nil
}

func (a *AlchemyProvider) ParsePayload(body []byte) ([]InboundTransfer, error) {
	var event struct {
		Event struct {
			Network  string `json:"network"`
			Activity []struct {
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
					LogIndex        string `json:"logIndex"`
					TransactionHash string `json:"transactionHash"`
					BlockHash       string `json:"blockHash"`
				} `json:"log"`
			} `json:"activity"`
		} `json:"event"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("alchemy parse: %w", err)
	}

	var transfers []InboundTransfer
	for _, act := range event.Event.Activity {
		blockNum := hexToUint64(act.BlockNum)
		logIndex := -1
		blockHash := ""

		if act.Log.LogIndex != "" {
			logIndex = int(hexToUint64(act.Log.LogIndex))
		}
		if act.Log.BlockHash != "" {
			blockHash = act.Log.BlockHash
		}

		amount := parseRawValue(act.RawContract.RawValue, act.Value, act.RawContract.Decimals)

		transfer := InboundTransfer{
			TxHash:      act.Hash,
			BlockNumber: blockNum,
			BlockHash:   blockHash,
			From:        strings.ToLower(act.FromAddress),
			To:          strings.ToLower(act.ToAddress),
			Amount:      amount,
			Asset:       act.Asset,
			LogIndex:    logIndex,
			Timestamp:   time.Now().UTC(),
		}

		if act.Category == "token" && act.RawContract.Address != "" {
			transfer.Token = &types.Token{
				Contract: act.RawContract.Address,
				Symbol:   act.Asset,
				Decimals: uint8(act.RawContract.Decimals),
			}
		}

		transfers = append(transfers, transfer)
	}
	return transfers, nil
}

func hexToUint64(s string) uint64 {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0
	}
	n := new(big.Int)
	n.SetString(s, 16)
	return n.Uint64()
}

func parseRawValue(rawHex string, humanValue float64, decimals int) *big.Int {
	if rawHex != "" && rawHex != "0x" {
		s := strings.TrimPrefix(rawHex, "0x")
		n := new(big.Int)
		n.SetString(s, 16)
		return n
	}
	if decimals > 0 {
		d := new(big.Float).SetFloat64(humanValue)
		mul := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
		d.Mul(d, new(big.Float).SetInt(mul))
		result, _ := d.Int(nil)
		return result
	}
	weiPerEth := new(big.Float).SetFloat64(humanValue)
	weiPerEth.Mul(weiPerEth, new(big.Float).SetFloat64(1e18))
	result, _ := weiPerEth.Int(nil)
	return result
}
```

- [ ] **Step 2: Write test for payload parsing and signature verification**

Create `back/app/services/ingest/providers/alchemy_test.go`:

```go
package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlchemyVerifyInbound(t *testing.T) {
	provider := NewAlchemyProvider("test-token")
	secret := "test-signing-key"
	body := []byte(`{"test": "payload"}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	validSig := hex.EncodeToString(h.Sum(nil))

	headers := http.Header{}
	headers.Set("X-Alchemy-Signature", validSig)
	ok, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.True(t, ok)

	headers.Set("X-Alchemy-Signature", "invalid")
	ok, err = provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, ok)

	headers.Del("X-Alchemy-Signature")
	_, err = provider.VerifyInbound(headers, body, secret)
	assert.Error(t, err)
}

func TestAlchemyParsePayload(t *testing.T) {
	provider := NewAlchemyProvider("test-token")
	payload := []byte(`{
		"webhookId": "wh_test",
		"id": "whevt_test",
		"type": "ADDRESS_ACTIVITY",
		"event": {
			"network": "ETH_MAINNET",
			"activity": [{
				"blockNum": "0x100",
				"hash": "0xabc123",
				"fromAddress": "0x1111111111111111111111111111111111111111",
				"toAddress": "0x2222222222222222222222222222222222222222",
				"value": 1.5,
				"asset": "ETH",
				"category": "external",
				"rawContract": {"rawValue": "0x14d1120d7b160000", "address": "", "decimals": 0},
				"log": {"logIndex": "", "transactionHash": "0xabc123", "blockHash": "0xblock1"}
			}]
		}
	}`)

	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	assert.Equal(t, "0xabc123", transfers[0].TxHash)
	assert.Equal(t, uint64(256), transfers[0].BlockNumber)
	assert.Equal(t, "ETH", transfers[0].Asset)
	assert.Equal(t, -1, transfers[0].LogIndex)
	assert.Nil(t, transfers[0].Token)
}

func TestAlchemyParsePayloadERC20(t *testing.T) {
	provider := NewAlchemyProvider("test-token")
	payload := []byte(`{
		"webhookId": "wh_test",
		"id": "whevt_test",
		"type": "ADDRESS_ACTIVITY",
		"event": {
			"network": "ETH_MAINNET",
			"activity": [{
				"blockNum": "0x200",
				"hash": "0xdef456",
				"fromAddress": "0x3333333333333333333333333333333333333333",
				"toAddress": "0x4444444444444444444444444444444444444444",
				"value": 100.0,
				"asset": "USDC",
				"category": "token",
				"rawContract": {"rawValue": "0x05f5e100", "address": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", "decimals": 6},
				"log": {"logIndex": "0x5", "transactionHash": "0xdef456", "blockHash": "0xblock2"}
			}]
		}
	}`)

	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	assert.Equal(t, 5, transfers[0].LogIndex)
	assert.NotNil(t, transfers[0].Token)
	assert.Equal(t, "USDC", transfers[0].Token.Symbol)
}
```

- [ ] **Step 3: Run tests**

```bash
cd back && go test ./app/services/ingest/providers/... -v -count=1
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add back/app/services/ingest/providers/alchemy.go back/app/services/ingest/providers/alchemy_test.go
git commit -m "feat: add Alchemy webhook provider with address sync and payload parsing"
```

---

### Task 8: Helius provider

**Files:**
- Create: `back/app/services/ingest/providers/helius.go`
- Create: `back/app/services/ingest/providers/helius_test.go`

- [ ] **Step 1: Write the Helius provider**

Create `back/app/services/ingest/providers/helius.go`. Key differences from Alchemy:
- API base: `https://api-mainnet.helius-rpc.com`
- Auth: `api-key` query parameter for management, `authHeader` for inbound verification
- Create: `POST /v0/webhooks?api-key=KEY` with `webhookURL`, `webhookType`, `accountAddresses`, `transactionTypes: ["ANY"]`, `authHeader`
- Update: `PUT /v0/webhooks/{webhookID}?api-key=KEY` with full body (replaces all fields)
- VerifyInbound: constant-time compare of Authorization header against stored authHeader
- ParsePayload: extract `nativeTransfers[]` and `tokenTransfers[]` from enhanced payload

Implementation should follow the same HTTP client pattern as Alchemy. The enhanced payload has `nativeTransfers[].fromUserAccount`, `nativeTransfers[].toUserAccount`, `nativeTransfers[].amount` (in lamports), and `tokenTransfers[].fromUserAccount`, `tokenTransfers[].toUserAccount`, `tokenTransfers[].tokenAmount`, `tokenTransfers[].mint`.

- [ ] **Step 2: Write tests for parsing and auth verification**

Test the `authHeader` verification (constant-time compare) and enhanced payload parsing.

- [ ] **Step 3: Run tests and commit**

```bash
cd back && go test ./app/services/ingest/providers/... -v -count=1
git add back/app/services/ingest/providers/helius.go back/app/services/ingest/providers/helius_test.go
git commit -m "feat: add Helius webhook provider for Solana"
```

---

### Task 9: QuickNode provider

**Files:**
- Create: `back/app/services/ingest/providers/quicknode.go`
- Create: `back/app/services/ingest/providers/quicknode_test.go`

- [ ] **Step 1: Write the QuickNode Streams provider**

Create `back/app/services/ingest/providers/quicknode.go`. Key differences:
- API base: `https://api.quicknode.com/streams/rest/v1/streams`
- Auth: `x-api-key` header for management
- Create: `POST /streams` with `name`, `network` (e.g. `bitcoin-mainnet`), `dataset: "block"`, `filter_function` (base64-encoded JS), `destination` (webhook URL), `status: "active"`
- Update: `PATCH /streams/{id}` with updated `filter_function`
- VerifyInbound: HMAC verification of body
- ParsePayload: parse the filtered output from the JS function (format: `{txid, blockNumber, blockHash, toAddress, amount, timestamp}`)

The `filter_function` is a JavaScript function that checks vout addresses against a hardcoded list. `SyncAddresses` re-generates this function with the new address list and base64-encodes it.

JS filter template:
```javascript
function main(stream) {
  const addresses = new Set([%ADDRESSES%]);
  const block = stream.data[0];
  const results = [];
  for (const tx of (block.tx || [])) {
    for (const vout of (tx.vout || [])) {
      const addr = vout.scriptPubKey && (vout.scriptPubKey.address || (vout.scriptPubKey.addresses && vout.scriptPubKey.addresses[0]));
      if (addr && addresses.has(addr)) {
        results.push({txid: tx.txid, blockNumber: block.height, blockHash: block.hash, toAddress: addr, amount: vout.value, timestamp: block.time});
      }
    }
  }
  return results.length > 0 ? results : null;
}
```

- [ ] **Step 2: Write tests for filter generation, parsing, and HMAC verification**

- [ ] **Step 3: Run tests and commit**

```bash
cd back && go test ./app/services/ingest/providers/... -v -count=1
git add back/app/services/ingest/providers/quicknode.go back/app/services/ingest/providers/quicknode_test.go
git commit -m "feat: add QuickNode Streams provider for Bitcoin"
```

---

## Phase 3: Ingest Service & Handler

### Task 10: IngestService

**Files:**
- Create: `back/app/services/ingest/service.go`

- [ ] **Step 1: Write the IngestService**

This service receives parsed `InboundTransfer`s and converts them to deposits. It reuses the same logic as `deposit.Service.processTransfer` but accepts the normalized `InboundTransfer` type.

```go
package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

type Service struct {
	rdb         *redis.Client
	registry    *chain.Registry
	webhookSvc  *webhook.Service
	addressRepo repositories.AddressRepository
	txRepo      repositories.TransactionRepository
}

func NewService(rdb *redis.Client, registry *chain.Registry, webhookSvc *webhook.Service, addressRepo repositories.AddressRepository, txRepo repositories.TransactionRepository) *Service {
	return &Service{rdb: rdb, registry: registry, webhookSvc: webhookSvc, addressRepo: addressRepo, txRepo: txRepo}
}

func (s *Service) ProcessTransfers(ctx context.Context, chainID string, transfers []providers.InboundTransfer) error {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return fmt.Errorf("unknown chain %s: %w", chainID, err)
	}

	for _, transfer := range transfers {
		if err := s.processTransfer(ctx, chainID, adapter, transfer); err != nil {
			slog.Error("ingest process transfer", "tx", transfer.TxHash, "error", err)
		}
	}
	return nil
}

func (s *Service) processTransfer(ctx context.Context, chainID string, adapter types.Chain, transfer providers.InboundTransfer) error {
	if s.rdb != nil {
		isMine, err := s.rdb.SIsMember(ctx, "vault:addresses:"+chainID, transfer.To).Result()
		if err != nil || !isMine {
			return nil
		}
	} else {
		count, err := s.addressRepo.CountByChainAndAddress(chainID, transfer.To)
		if err != nil || count == 0 {
			return nil
		}
	}

	addr, err := s.addressRepo.FindByChainAndAddress(chainID, transfer.To)
	if err != nil || addr == nil {
		return fmt.Errorf("lookup address: %w", err)
	}

	exists, err := s.txRepo.CountByChainTxHashAndLogIndex(chainID, transfer.TxHash, transfer.LogIndex, "deposit")
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	asset := adapter.NativeAsset()
	var tokenContract string
	if transfer.Token != nil {
		asset = transfer.Token.Symbol
		tokenContract = transfer.Token.Contract
	}

	tx := &models.Transaction{
		ID:             uuid.New(),
		AddressID:      &addr.ID,
		WalletID:       addr.WalletID,
		ExternalUserID: addr.ExternalUserID,
		Chain:          chainID,
		TxType:         "deposit",
		TxHash:         transfer.TxHash,
		FromAddress:    transfer.From,
		ToAddress:      transfer.To,
		Amount:         transfer.Amount.String(),
		Asset:          asset,
		TokenContract:  tokenContract,
		Confirmations:  0,
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Status:         string(types.TxStatusPending),
		BlockNumber:    int64(transfer.BlockNumber),
		BlockHash:      transfer.BlockHash,
		LogIndex:       transfer.LogIndex,
	}

	if err := s.txRepo.Create(tx); err != nil {
		return fmt.Errorf("insert tx: %w", err)
	}

	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, tx)

	slog.Info("deposit detected via webhook",
		"chain", chainID, "tx", transfer.TxHash,
		"user", addr.ExternalUserID, "asset", asset,
		"amount", transfer.Amount.String())
	return nil
}
```

- [ ] **Step 2: Commit**

```bash
git add back/app/services/ingest/service.go
git commit -m "feat: add IngestService for processing webhook transfers"
```

---

### Task 11: Ingest HTTP handler

**Files:**
- Create: `back/app/services/ingest/handler.go`

- [ ] **Step 1: Write the handler**

Create `back/app/services/ingest/handler.go`:

```go
package ingest

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

type Handler struct {
	service         *Service
	subscriptionRepo repositories.WebhookSubscriptionRepository
	providers       map[string]providers.WebhookProvider
}

func NewHandler(service *Service, subRepo repositories.WebhookSubscriptionRepository, provs map[string]providers.WebhookProvider) *Handler {
	return &Handler{service: service, subscriptionRepo: subRepo, providers: provs}
}
```

The handler's `HandleIngest` method reads raw body, looks up subscription, verifies signature, parses payload, and calls `service.ProcessTransfers`. It follows the Goravel controller pattern used in `routes/api.go`. Return 200 on success, 401 on bad signature, 400 on parse error.

- [ ] **Step 2: Register route in `routes/api.go`**

Add to `routes/api.go` an unauthenticated group for ingest:

```go
facades.Route().Prefix("/v1/webhooks/ingest").Group(func(router route.Router) {
    router.Post("/:provider/:chainID", controllers.HandleWebhookIngest)
})
```

- [ ] **Step 3: Commit**

```bash
git add back/app/services/ingest/handler.go back/routes/api.go
git commit -m "feat: add webhook ingest HTTP handler and route"
```

---

## Phase 4: Block Height Providers

### Task 12: BlockHeightProvider interface and implementations

**Files:**
- Create: `back/app/services/blockheight/provider.go`
- Create: `back/app/services/blockheight/etherscan.go`
- Create: `back/app/services/blockheight/blockstream.go`
- Create: `back/app/services/blockheight/solana_public.go`
- Create: `back/app/services/blockheight/provider_test.go`

- [ ] **Step 1: Write the interface**

Create `back/app/services/blockheight/provider.go`:

```go
package blockheight

import "context"

type Provider interface {
	GetBlockHeight(ctx context.Context, chainID string) (uint64, error)
}
```

- [ ] **Step 2: Write EtherscanProvider**

Create `back/app/services/blockheight/etherscan.go`. HTTP GET to `https://api.etherscan.io/v2/api?chainid={id}&module=proxy&action=eth_blockNumber&apikey={key}`. Parse hex result. Map chain IDs: `eth` → `1`, `polygon` → `137`, `teth` → `11155111`, etc.

- [ ] **Step 3: Write BlockstreamProvider**

Create `back/app/services/blockheight/blockstream.go`. HTTP GET to `https://blockstream.info/api/blocks/tip/height`. Returns plain integer. For testnet: `https://blockstream.info/testnet/api/blocks/tip/height`.

- [ ] **Step 4: Write SolanaPublicProvider**

Create `back/app/services/blockheight/solana_public.go`. JSON-RPC POST to `https://api.mainnet-beta.solana.com` with `getSlot` method and `finalized` commitment. For devnet: `https://api.devnet.solana.com`.

- [ ] **Step 5: Write tests**

Test each implementation with mock HTTP servers.

- [ ] **Step 6: Run tests and commit**

```bash
cd back && go test ./app/services/blockheight/... -v -count=1
git add back/app/services/blockheight/
git commit -m "feat: add free block height providers (Etherscan, Blockstream, Solana)"
```

---

## Phase 5: Confirmation Tracker

### Task 13: Extract and enhance confirmation logic

**Files:**
- Create: `back/app/services/deposit/confirmation.go`
- Modify: `back/app/services/deposit/service.go`

- [ ] **Step 1: Extract `updateConfirmations` into `confirmation.go`**

Move the `updateConfirmations` method from `service.go` to a new `confirmation.go` file. Add a new `RunConfirmationCheck` function that:
1. Gets all active chains from the registry
2. For each chain with pending deposits, fetches block height from `BlockHeightProvider`
3. Calls `updateConfirmations` with the fetched height
4. Implements staleness detection (warn if height unchanged for 5+ checks)
5. Falls back to RPC `GetLatestBlock` after 3 consecutive explorer failures

- [ ] **Step 2: Add `confirmation_tracker` mode to `main.go`**

In `back/main.go`, add a new case in the `switch mode` block:

```go
case "confirmation_tracker":
    lambda.Start(handleConfirmationTracker)
```

With handler:
```go
func handleConfirmationTracker(ctx context.Context) error {
    slog.Info("confirmation tracker triggered")
    return c.DepositService.RunConfirmationCheck(ctx)
}
```

- [ ] **Step 3: Commit**

```bash
git add back/app/services/deposit/confirmation.go back/app/services/deposit/service.go back/main.go
git commit -m "feat: add confirmation tracker with free block height APIs"
```

---

## Phase 6: WebhookSyncService

### Task 14: WebhookSyncService

**Files:**
- Create: `back/app/services/webhooksync/service.go`

- [ ] **Step 1: Write the sync service**

Handles address sync on wallet create/deactivate. Serializes calls per subscription. Updates `sync_status` and `synced_addresses_hash`.

```go
package webhooksync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

type Service struct {
	subscriptionRepo repositories.WebhookSubscriptionRepository
	addressRepo      repositories.AddressRepository
	providers        map[string]providers.WebhookProvider
	mu               sync.Map // per-subscription lock
}

func NewService(subRepo repositories.WebhookSubscriptionRepository, addrRepo repositories.AddressRepository, provs map[string]providers.WebhookProvider) *Service {
	return &Service{subscriptionRepo: subRepo, addressRepo: addrRepo, providers: provs}
}

func (s *Service) SyncChainAddresses(ctx context.Context, chainID string) error {
	sub, err := s.subscriptionRepo.FindByChainID(chainID)
	if err != nil || sub == nil {
		slog.Warn("no webhook subscription for chain", "chain", chainID)
		return nil
	}

	lockKey := sub.ID.String()
	mu, _ := s.mu.LoadOrStore(lockKey, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "pending"})

	addresses, err := s.addressRepo.PluckActiveAddresses(chainID)
	if err != nil {
		s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("load addresses for %s: %w", chainID, err)
	}

	provider, ok := s.providers[sub.Provider]
	if !ok {
		s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("unknown provider %s", sub.Provider)
	}

	secret, err := facades.Crypt().DecryptString(sub.SigningSecret)
	if err != nil {
		slog.Error("decrypt signing secret failed", "chain", chainID, "error", err)
	}
	_ = secret

	if err := provider.SyncAddresses(ctx, sub.ProviderWebhookID, addresses); err != nil {
		s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("sync addresses for %s: %w", chainID, err)
	}

	now := time.Now().UTC()
	hash := hashAddresses(addresses)
	s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{
		"sync_status":           "synced",
		"synced_addresses_hash": hash,
		"last_synced_at":        now,
	})

	slog.Info("addresses synced to provider", "chain", chainID, "provider", sub.Provider, "count", len(addresses))
	return nil
}

func hashAddresses(addresses []string) string {
	sorted := make([]string, len(addresses))
	copy(sorted, addresses)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return fmt.Sprintf("%x", h)
}
```

- [ ] **Step 2: Commit**

```bash
git add back/app/services/webhooksync/service.go
git commit -m "feat: add WebhookSyncService for address sync to providers"
```

---

### Task 15: Reconciler

**Files:**
- Create: `back/app/services/webhooksync/reconciler.go`

- [ ] **Step 1: Write the reconciler**

Runs daily via Lambda. Calls `SyncChainAddresses` for every active subscription.

- [ ] **Step 2: Add `webhook_reconciler` Lambda mode to `main.go`**

- [ ] **Step 3: Commit**

```bash
git add back/app/services/webhooksync/reconciler.go back/main.go
git commit -m "feat: add daily webhook reconciler for address drift detection"
```

---

## Phase 7: Wiring & Integration

### Task 16: Container wiring

**Files:**
- Modify: `back/app/container/container.go`

- [ ] **Step 1: Add new fields to Container struct**

Add:
```go
WebhookSubscriptionRepo repositories.WebhookSubscriptionRepository
IngestService           *ingest.Service
WebhookSyncService      *webhooksync.Service
WebhookProviders        map[string]providers.WebhookProvider
BlockHeightProviders    map[string]blockheight.Provider
```

- [ ] **Step 2: Wire in Boot()**

After existing repository instantiation:
```go
c.WebhookSubscriptionRepo = repositories.NewWebhookSubscriptionRepository()
```

After chain registry setup, build providers:
```go
providerMap := make(map[string]providers.WebhookProvider)
if token := os.Getenv("ALCHEMY_AUTH_TOKEN"); token != "" {
    providerMap["alchemy"] = providers.NewAlchemyProvider(token)
}
if key := os.Getenv("HELIUS_API_KEY"); key != "" {
    providerMap["helius"] = providers.NewHeliusProvider(key)
}
if key := os.Getenv("QUICKNODE_API_KEY"); key != "" {
    providerMap["quicknode"] = providers.NewQuickNodeProvider(key)
}
c.WebhookProviders = providerMap
```

Wire services:
```go
c.IngestService = ingest.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)
c.WebhookSyncService = webhooksync.NewService(c.WebhookSubscriptionRepo, c.AddressRepo, providerMap)
```

Wire block height providers:
```go
bhMap := make(map[string]blockheight.Provider)
bhMap["etherscan"] = blockheight.NewEtherscanProvider(os.Getenv("ETHERSCAN_API_KEY"))
bhMap["blockstream"] = blockheight.NewBlockstreamProvider()
bhMap["solana"] = blockheight.NewSolanaPublicProvider()
c.BlockHeightProviders = bhMap
```

- [ ] **Step 3: Commit**

```bash
git add back/app/container/container.go
git commit -m "feat: wire ingest, sync, and block height services in container"
```

---

### Task 17: Integrate WebhookSyncService with WalletService

**Files:**
- Modify: `back/app/services/wallet/service.go`

- [ ] **Step 1: Add WebhookSyncService dependency**

Add `webhookSyncSvc *webhooksync.Service` to the `Service` struct and `NewService` constructor.

- [ ] **Step 2: Call SyncChainAddresses after wallet creation**

In `CreateWallet`, after the wallet and address are persisted and `RefreshAddressCache` is called, add:

```go
if s.webhookSyncSvc != nil {
    go func() {
        if err := s.webhookSyncSvc.SyncChainAddresses(context.Background(), chainID); err != nil {
            slog.Error("webhook address sync failed", "chain", chainID, "error", err)
        }
    }()
}
```

Non-blocking — runs in a goroutine so wallet creation is not delayed by provider API calls.

- [ ] **Step 3: Update container wiring**

Update the `wallet.NewService` call in `container.go` to pass `c.WebhookSyncService`.

- [ ] **Step 4: Run existing wallet tests**

```bash
cd back && go test ./app/services/wallet/... -v -count=1
```

Expected: existing tests pass (sync service can be nil in tests).

- [ ] **Step 5: Commit**

```bash
git add back/app/services/wallet/service.go back/app/container/container.go
git commit -m "feat: sync webhook addresses on wallet creation"
```

---

### Task 18: Update template.yaml

**Files:**
- Modify: `back/template.yaml`

- [ ] **Step 1: Add ConfirmationTrackerFunction**

Add new Lambda function with `LAMBDA_MODE: confirmation_tracker`, EventBridge schedule `rate(1 minute)`:

```yaml
ConfirmationTrackerFunction:
  Type: AWS::Serverless::Function
  Properties:
    FunctionName: !Sub "vault-confirmation-tracker-${Environment}"
    Handler: bootstrap
    Runtime: provided.al2023
    Architectures: [arm64]
    MemorySize: 256
    Timeout: 60
    Environment:
      Variables:
        LAMBDA_MODE: confirmation_tracker
        # ... shared env vars ...
    Events:
      Schedule:
        Type: Schedule
        Properties:
          Name: !Sub "vault-confirmation-tracker-${Environment}"
          Schedule: rate(1 minute)
          Enabled: true
```

- [ ] **Step 2: Add WebhookReconcilerFunction**

Similar Lambda with `LAMBDA_MODE: webhook_reconciler`, EventBridge schedule `rate(1 day)`.

- [ ] **Step 3: Add new env vars to all functions**

Add `ALCHEMY_AUTH_TOKEN`, `HELIUS_API_KEY`, `QUICKNODE_API_KEY`, `ETHERSCAN_API_KEY`, `WEBHOOK_INGEST_BASE_URL` to the Globals or per-function environment.

- [ ] **Step 4: Commit**

```bash
git add back/template.yaml
git commit -m "feat: add confirmation tracker and reconciler Lambda definitions"
```

---

## Phase 8: Seed Data & Documentation

### Task 19: Seed webhook subscriptions

**Files:**
- Modify: `back/database/seeds/seeder.go`

- [ ] **Step 1: Add webhook subscription seeds**

Add seed data for webhook subscriptions. These are created manually via provider dashboards or a setup script, but the seeder documents the expected state:

```go
// Webhook subscriptions — populated after provider setup
// Seeds serve as documentation of expected configuration
```

- [ ] **Step 2: Commit**

```bash
git add back/database/seeds/seeder.go
git commit -m "docs: document expected webhook subscription seed data"
```

---

### Task 20: Update RPC_PROVIDERS.md

**Files:**
- Modify: `back/docs/RPC_PROVIDERS.md`

- [ ] **Step 1: Add webhook provider section**

Add a new section documenting the webhook providers (Alchemy, Helius, QuickNode), their setup process, API keys needed, and how they reduce RPC usage.

- [ ] **Step 2: Update cost estimation**

Update the cost table to reflect that block scanning is eliminated.

- [ ] **Step 3: Commit**

```bash
git add back/docs/RPC_PROVIDERS.md
git commit -m "docs: update RPC providers guide with webhook-based monitoring"
```

---

### Task 21: Add .env.example entries

**Files:**
- Modify: `back/.env.example`

- [ ] **Step 1: Add new environment variables**

```bash
# Webhook Providers (deposit monitoring)
ALCHEMY_AUTH_TOKEN=
HELIUS_API_KEY=
QUICKNODE_API_KEY=
ETHERSCAN_API_KEY=
WEBHOOK_INGEST_BASE_URL=http://localhost:8080
```

- [ ] **Step 2: Commit**

```bash
git add back/.env.example
git commit -m "docs: add webhook provider env vars to .env.example"
```

---

## Phase 9: Integration Testing

### Task 22: End-to-end ingest test

**Files:**
- Create: `back/app/services/ingest/service_test.go`

- [ ] **Step 1: Write integration test**

Test the full flow: craft an Alchemy-style payload → call the ingest handler → verify deposit is created in DB → verify webhook event is enqueued.

Use `testutil.BootTest()` and mock the signature verification to focus on the processing logic.

- [ ] **Step 2: Run all tests**

```bash
cd back && go test ./... -v -count=1
```

Expected: all tests pass including new ones.

- [ ] **Step 3: Commit**

```bash
git add back/app/services/ingest/service_test.go
git commit -m "test: add end-to-end ingest service integration test"
```
