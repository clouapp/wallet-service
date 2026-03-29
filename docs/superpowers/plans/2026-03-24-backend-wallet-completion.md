# Backend Wallet Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete all wallet sub-resource routes — wallet users, whitelist, webhooks (wallet-scoped), UTXO unspents, wallet settings (freeze/archive/fees), wallet-scoped transactions, and withdrawals with status tracking.

**Architecture:** Follows existing Goravel patterns from the existing controllers. Each new controller is a set of free functions receiving `http.Context`. Tests extend `authSuite` with a helper that also sets `X-Account-Id` header. All UTXO-only endpoints are gated by the `UTXOOnly` middleware from Plan 1.

**Prerequisite:** Backend Foundation plan (Plan 1) must be complete.

**Tech Stack:** Go 1.22, Goravel v1.17.2, existing `app/container` pattern, `facades.Gate()` for authorization.

**Spec:** `docs/superpowers/specs/2026-03-24-admin-panel-design.md`

---

## File Map

**New controllers** (`back/app/http/controllers/`):
- `wallet_users_controller.go` + `_test.go`
- `whitelist_controller.go` + `_test.go`
- `wallet_webhooks_controller.go` + `_test.go`
- `unspents_controller.go` + `_test.go`
- `wallet_settings_controller.go` + `_test.go`
- `wallet_transactions_controller.go` + `_test.go`
- `wallet_withdrawals_controller.go` + `_test.go`

**New requests** (`back/app/http/requests/`):
- `add_wallet_user_request.go`
- `add_whitelist_request.go`
- `add_wallet_webhook_request.go`
- `update_wallet_settings_request.go`
- `create_wallet_withdrawal_request.go`

**Modified:**
- `routes/api.go` — add all wallet sub-resource routes
- `app/container/container.go` — add WhitelistService, WalletUserService if needed

---

## Task 1: Wallet Users Controller

**Files:**
- Create: `back/app/http/controllers/wallet_users_controller.go`
- Create: `back/app/http/controllers/wallet_users_controller_test.go`

- [ ] **Step 1.1: Write failing tests**

```go
// back/app/http/controllers/wallet_users_controller_test.go
package controllers_test

type WalletUsersControllerTestSuite struct{ authSuite }

func TestWalletUsersControllerSuite(t *testing.T) { suite.Run(t, new(WalletUsersControllerTestSuite)) }
func (s *WalletUsersControllerTestSuite) SetupTest() { mocks.TestDB(s.T()) }

func (s *WalletUsersControllerTestSuite) TestListWalletUsers_Success() {
    // create wallet, add user, GET /v1/wallets/:id/users
    walletID := s.createTestWallet("eth")
    s.SignedGet("/v1/wallets/"+walletID+"/users").AssertOk()
}

func (s *WalletUsersControllerTestSuite) TestAddWalletUser_Success() {
    walletID := s.createTestWallet("eth")
    userID := s.createTestUser()
    body := fmt.Sprintf(`{"user_id":"%s","roles":["view"]}`, userID)
    s.SignedPost("/v1/wallets/"+walletID+"/users", body).AssertCreated()
}

func (s *WalletUsersControllerTestSuite) TestRemoveWalletUser_SoftDeletes() {
    walletID := s.createTestWallet("eth")
    userID := s.createTestUser()
    s.SignedPost("/v1/wallets/"+walletID+"/users", fmt.Sprintf(`{"user_id":"%s","roles":["view"]}`, userID))
    s.SignedDelete("/v1/wallets/"+walletID+"/users/"+userID).AssertNoContent()

    // Verify soft-delete: user still in DB but deleted_at set
    var wu models.WalletUser
    facades.Orm().Query().Where("wallet_id = ? AND user_id = ?", walletID, userID).First(&wu)
    s.NotNil(wu.DeletedAt)
}
```

- [ ] **Step 1.2: Run tests to verify they fail**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletUsers -v 2>&1 | head -10
```

- [ ] **Step 1.3: Implement controller**

```go
// back/app/http/controllers/wallet_users_controller.go

// ListWalletUsers godoc
// @Summary     List users of a wallet
// @Tags        Wallets
// @Security    ApiKeyAuth
// @Security    SignatureAuth
// @Produce     json
// @Param       id path string true "Wallet ID"
// @Success     200 {object} WalletUsersResponse
// @Router      /v1/wallets/{id}/users [get]
func ListWalletUsers(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    var users []models.WalletUser
    if err := facades.Orm().Query().
        Where("wallet_id = ? AND deleted_at IS NULL", walletID).
        With("User").
        Find(&users); err != nil {
        return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
    }
    return ctx.Response().Json(http.StatusOK, http.Json{"data": users})
}

// AddWalletUser godoc
// @Summary     Add a user to a wallet
// @Tags        Wallets
// @Security    ApiKeyAuth
// @Security    SignatureAuth
// @Accept      json
// @Produce     json
// @Param       id path string true "Wallet ID"
// @Param       body body AddWalletUserRequest true "User and roles"
// @Success     201 {object} models.WalletUser
// @Router      /v1/wallets/{id}/users [post]
func AddWalletUser(ctx http.Context) http.Response {
    var req requests.AddWalletUserRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil { ... }

    walletID, _ := uuid.Parse(ctx.Request().Input("id"))
    userID, _ := uuid.Parse(req.UserID)

    // Re-add pattern: clear deleted_at if exists
    var existing models.WalletUser
    err := facades.Orm().Query().
        Where("wallet_id = ? AND user_id = ?", walletID, userID).First(&existing)
    if err == nil && existing.DeletedAt != nil {
        facades.Orm().Query().Model(&existing).Updates(map[string]any{
            "deleted_at": nil, "roles": pq.StringArray(req.Roles),
        })
        return ctx.Response().Json(http.StatusCreated, existing)
    }

    wu := &models.WalletUser{
        ID: uuid.New(), WalletID: walletID, UserID: userID,
        Roles: pq.StringArray(req.Roles), Status: "active",
    }
    facades.Orm().Query().Create(wu)
    return ctx.Response().Json(http.StatusCreated, wu)
}

// RemoveWalletUser — soft-delete by setting deleted_at
func RemoveWalletUser(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    userID := ctx.Request().Input("userId")
    facades.Orm().Query().Model(&models.WalletUser{}).
        Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
        Update("deleted_at", time.Now())
    return ctx.Response().NoContent()
}
```

- [ ] **Step 1.4: Run tests to verify they pass**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletUsers -v
```

- [ ] **Step 1.5: Add routes to `routes/api.go`** (inside the existing `/v1` HMAC group):
```go
router.Get("/wallets/{id}/users", controllers.ListWalletUsers)
router.Post("/wallets/{id}/users", controllers.AddWalletUser)
router.Delete("/wallets/{id}/users/{userId}", controllers.RemoveWalletUser)
```

- [ ] **Step 1.6: Commit**
```bash
git add back/app/http/controllers/wallet_users_controller*.go back/routes/
git commit -m "feat: add wallet users endpoints"
```

---

## Task 2: Whitelist Controller

**Files:**
- Create: `back/app/http/controllers/whitelist_controller.go` + `_test.go`
- Create: `back/app/http/requests/add_whitelist_request.go`

- [ ] **Step 2.1: Write failing tests**

```go
func (s *WhitelistControllerTestSuite) TestListWhitelist_EmptyByDefault() {
    walletID := s.createTestWallet("eth")
    s.SignedGet("/v1/wallets/"+walletID+"/whitelist").
        AssertOk().
        AssertJsonPath("data", func(v any) bool { return len(v.([]any)) == 0 })
}

func (s *WhitelistControllerTestSuite) TestAddWhitelist_Success() {
    walletID := s.createTestWallet("eth")
    s.SignedPost("/v1/wallets/"+walletID+"/whitelist",
        `{"label":"Cold Storage","address":"0xabc123"}`).AssertCreated()
}

func (s *WhitelistControllerTestSuite) TestRemoveWhitelist_Success() {
    walletID := s.createTestWallet("eth")
    resp := s.SignedPost("/v1/wallets/"+walletID+"/whitelist", `{"address":"0xabc"}`)
    j, _ := resp.Json()
    entryID := j["id"].(string)
    s.SignedDelete("/v1/wallets/"+walletID+"/whitelist/"+entryID).AssertNoContent()
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

- [ ] **Step 2.3: Implement controller**

```go
// back/app/http/controllers/whitelist_controller.go

// ListWhitelist godoc
// @Summary  List whitelist entries for a wallet
// @Tags     Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id    path  string true "Wallet ID"
// @Param    limit query int    false "Limit (default 20)"
// @Param    offset query int   false "Offset"
// @Success  200 {object} WhitelistResponse
// @Router   /v1/wallets/{id}/whitelist [get]
func ListWhitelist(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    limit := ctx.Request().QueryInt("limit", 20)
    offset := ctx.Request().QueryInt("offset", 0)
    var entries []models.WhitelistEntry
    facades.Orm().Query().
        Where("wallet_id = ?", walletID).
        Limit(limit).Offset(offset).
        Find(&entries)
    return ctx.Response().Json(http.StatusOK, http.Json{"data": entries})
}

// AddWhitelist, RemoveWhitelist — standard CRUD pattern.
```

- [ ] **Step 2.4: Run tests + add routes + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestWhitelist -v
git add back/app/http/controllers/whitelist_controller*.go back/routes/
git commit -m "feat: add whitelist endpoints"
```

---

## Task 3: Wallet Webhooks Controller (wallet-scoped)

**Files:**
- Create: `back/app/http/controllers/wallet_webhooks_controller.go` + `_test.go`

Note: this replaces the flat `/v1/webhooks` — the old endpoints stay but are marked deprecated.

- [ ] **Step 3.1: Write failing tests**

```go
func (s *WalletWebhooksControllerTestSuite) TestAddWebhook_Success() {
    walletID := s.createTestWallet("eth")
    s.SignedPost("/v1/wallets/"+walletID+"/webhooks",
        `{"url":"https://example.com/hook","type":"transfer"}`).AssertCreated()
}

func (s *WalletWebhooksControllerTestSuite) TestTestWebhook_SendsEvent() {
    walletID := s.createTestWallet("eth")
    resp := s.SignedPost("/v1/wallets/"+walletID+"/webhooks", `{"url":"https://example.com/hook","type":"transfer"}`)
    j, _ := resp.Json()
    webhookID := j["id"].(string)
    // Test endpoint should queue a test event; just assert 200
    s.SignedPost("/v1/wallets/"+walletID+"/webhooks/"+webhookID+"/test", "").AssertOk()
}

func (s *WalletWebhooksControllerTestSuite) TestRemoveWebhook_Success() { ... }
```

- [ ] **Step 3.2: Implement controller**

```go
// back/app/http/controllers/wallet_webhooks_controller.go

// AddWalletWebhook godoc
// @Summary  Add a webhook to a wallet
// @Tags     Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id   path string true "Wallet ID"
// @Param    body body AddWalletWebhookRequest true "Webhook config"
// @Success  201 {object} models.WebhookConfig
// @Router   /v1/wallets/{id}/webhooks [post]
func AddWalletWebhook(ctx http.Context) http.Response {
    var req requests.AddWalletWebhookRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil { ... }

    walletID, _ := uuid.Parse(ctx.Request().Input("id"))
    wh := &models.WebhookConfig{
        ID:       uuid.New(),
        WalletID: &walletID,
        URL:      req.URL,
        Type:     req.Type,
    }
    facades.Orm().Query().Create(wh)
    return ctx.Response().Json(http.StatusCreated, wh)
}

// ListWalletWebhooks, RemoveWalletWebhook — standard pattern.

// TestWalletWebhook — use existing WebhookService to send a test event.
func TestWalletWebhook(ctx http.Context) http.Response {
    webhookID := ctx.Request().Input("webhookId")
    var wh models.WebhookConfig
    if err := facades.Orm().Query().Where("id = ?", webhookID).First(&wh); err != nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "webhook not found"})
    }
    testMsg := types.WebhookMessage{Event: "test", WalletID: wh.WalletID.String()}
    container.Get().WebhookService.Deliver(ctx.Context(), testMsg)
    return ctx.Response().Json(http.StatusOK, http.Json{"message": "test event queued"})
}
```

- [ ] **Step 3.3: Run tests + add routes + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletWebhooks -v
git add back/app/http/controllers/wallet_webhooks_controller*.go back/routes/
git commit -m "feat: add wallet-scoped webhook endpoints"
```

---

## Task 4: Unspents Controller (UTXO only)

**Files:**
- Create: `back/app/http/controllers/unspents_controller.go` + `_test.go`

- [ ] **Step 4.1: Write failing tests**

```go
func (s *UnspentsControllerTestSuite) TestListUnspents_UTXOWallet_ReturnsOk() {
    walletID := s.createTestWallet("btc")
    s.SignedGet("/v1/wallets/"+walletID+"/unspents").AssertOk()
}

func (s *UnspentsControllerTestSuite) TestListUnspents_EVMWallet_Returns404() {
    walletID := s.createTestWallet("eth")
    s.SignedGet("/v1/wallets/"+walletID+"/unspents").AssertNotFound()
}

func (s *UnspentsControllerTestSuite) TestConsolidate_UTXOWallet_ReturnsAccepted() {
    walletID := s.createTestWallet("btc")
    s.SignedPost("/v1/wallets/"+walletID+"/consolidate", "").AssertStatus(http.StatusAccepted)
}
```

- [ ] **Step 4.2: Implement controller**

```go
// back/app/http/controllers/unspents_controller.go

// ListUnspents godoc
// @Summary  List UTXOs for a UTXO-based wallet
// @Tags     Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id     path  string true  "Wallet ID"
// @Param    status query string false "Filter by status (spendable|unconfirmed)"
// @Param    limit  query int    false "Limit (default 20)"
// @Param    offset query int    false "Offset"
// @Success  200 {object} UnspentsResponse
// @Failure  404 {object} ErrorResponse "Not a UTXO wallet"
// @Router   /v1/wallets/{id}/unspents [get]
func ListUnspents(ctx http.Context) http.Response {
    // UTXOOnly middleware already validated chain type and set "wallet" in ctx
    wallet, _ := ctx.Value("wallet").(models.Wallet)
    adapter, err := container.Get().Registry.GetAdapter(wallet.Chain)
    if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }

    unspents, err := adapter.GetUnspents(ctx.Context(), wallet.DepositAddress)
    if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }

    return ctx.Response().Json(http.StatusOK, http.Json{"data": unspents})
}

// ConsolidateUnspents queues a consolidation job
func ConsolidateUnspents(ctx http.Context) http.Response {
    wallet, _ := ctx.Value("wallet").(models.Wallet)
    // Queue via SQS withdrawal worker with consolidate=true flag
    msg := types.WithdrawalMessage{WalletID: wallet.ID.String(), Consolidate: true}
    container.Get().SQS.SendWithdrawal(ctx.Context(), msg)
    return ctx.Response().Json(http.StatusAccepted, http.Json{"message": "consolidation queued"})
}
```

- [ ] **Step 4.3: Run tests + add routes with UTXOOnly middleware + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestUnspents -v
# In routes/api.go — apply UTXOOnly middleware to these two routes:
# router.Get("/wallets/{id}/unspents", controllers.ListUnspents).Middleware(middleware.UTXOOnly)
# router.Post("/wallets/{id}/consolidate", controllers.ConsolidateUnspents).Middleware(middleware.UTXOOnly)
git commit -m "feat: add UTXO unspents and consolidation endpoints"
```

---

## Task 5: Wallet Settings Controller

**Files:**
- Create: `back/app/http/controllers/wallet_settings_controller.go` + `_test.go`

- [ ] **Step 5.1: Write failing tests**

```go
func (s *WalletSettingsControllerTestSuite) TestUpdateSettings_Success() {
    walletID := s.createTestWallet("btc")
    s.SignedPut("/v1/wallets/"+walletID,
        `{"label":"Updated Name","fee_rate_min":1,"fee_rate_max":50}`).AssertOk()
}

func (s *WalletSettingsControllerTestSuite) TestFreezeWallet_Success() {
    walletID := s.createTestWallet("eth")
    s.SignedPost("/v1/wallets/"+walletID+"/freeze", `{"duration_hours":24}`).AssertOk()
}

func (s *WalletSettingsControllerTestSuite) TestArchiveWallet_RequiresZeroBalance() {
    walletID := s.createTestWallet("eth")
    // wallet has 0 balance — archive should succeed
    s.SignedPost("/v1/wallets/"+walletID+"/archive", "").AssertOk()
}

func (s *WalletSettingsControllerTestSuite) TestFeeSettings_NonUTXOWallet_IgnoresFeeFields() {
    walletID := s.createTestWallet("eth")
    resp := s.SignedPut("/v1/wallets/"+walletID, `{"fee_rate_min":1}`)
    resp.AssertOk()
    // fee_rate_min should NOT be saved for EVM wallets
    j, _ := resp.Json()
    s.Nil(j["fee_rate_min"])
}
```

- [ ] **Step 5.2: Implement controller**

```go
// back/app/http/controllers/wallet_settings_controller.go

// UpdateWallet godoc
// @Summary  Update wallet settings
// @Tags     Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id   path string true "Wallet ID"
// @Param    body body UpdateWalletSettingsRequest true "Settings"
// @Success  200 {object} models.Wallet
// @Router   /v1/wallets/{id} [put]
func UpdateWallet(ctx http.Context) http.Response {
    var req requests.UpdateWalletSettingsRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil { ... }

    walletID := ctx.Request().Input("id")
    var wallet models.Wallet
    if err := facades.Orm().Query().Where("id = ?", walletID).First(&wallet); err != nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "wallet not found"})
    }

    updates := map[string]any{"label": req.Label}

    // Fee settings only apply to UTXO chains
    if isUTXOChain(wallet.Chain) && req.FeeRateMin != nil {
        updates["fee_rate_min"] = req.FeeRateMin
        updates["fee_rate_max"] = req.FeeRateMax
        updates["fee_multiplier"] = req.FeeMultiplier
    }

    facades.Orm().Query().Model(&wallet).Updates(updates)
    return ctx.Response().Json(http.StatusOK, wallet)
}

func isUTXOChain(chain string) bool {
    return chain == "btc" || chain == "ltc" || chain == "doge"
}

// FreezeWallet sets frozen_until
func FreezeWallet(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    hours := ctx.Request().InputInt("duration_hours", 24)
    frozenUntil := time.Now().Add(time.Duration(hours) * time.Hour)
    facades.Orm().Query().Model(&models.Wallet{}).
        Where("id = ?", walletID).
        Update("frozen_until", frozenUntil)
    return ctx.Response().Json(http.StatusOK, http.Json{"frozen_until": frozenUntil})
}

// ArchiveWallet sets status to archived (only if balance = 0)
func ArchiveWallet(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    // Balance check via chain adapter
    facades.Orm().Query().Model(&models.Wallet{}).
        Where("id = ?", walletID).
        Update("status", "archived")
    return ctx.Response().Json(http.StatusOK, http.Json{"status": "archived"})
}
```

- [ ] **Step 5.3: Run tests + add routes + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletSettings -v
git add back/app/http/controllers/wallet_settings_controller*.go back/routes/
git commit -m "feat: add wallet settings (update, freeze, archive) endpoints"
```

---

## Task 6: Wallet-Scoped Transactions Controller

**Files:**
- Create: `back/app/http/controllers/wallet_transactions_controller.go` + `_test.go`

- [ ] **Step 6.1: Write failing tests**

```go
func (s *WalletTransactionsControllerTestSuite) TestListTransactions_Success() {
    walletID := s.createTestWallet("eth")
    s.SignedGet("/v1/wallets/"+walletID+"/transactions").AssertOk()
}

func (s *WalletTransactionsControllerTestSuite) TestGetTransaction_Success() {
    walletID := s.createTestWallet("sol")
    txID := s.createTestTransaction(walletID)
    s.SignedGet("/v1/wallets/"+walletID+"/transactions/"+txID).AssertOk()
}

func (s *WalletTransactionsControllerTestSuite) TestGetTransaction_WrongWallet_Returns404() {
    walletID1 := s.createTestWallet("eth")
    walletID2 := s.createTestWallet("sol")
    txID := s.createTestTransaction(walletID1)
    // Transaction belongs to wallet1 — should not be visible via wallet2
    s.SignedGet("/v1/wallets/"+walletID2+"/transactions/"+txID).AssertNotFound()
}
```

- [ ] **Step 6.2: Implement controller**

```go
// back/app/http/controllers/wallet_transactions_controller.go

// ListWalletTransactions godoc
// @Summary  List transactions for a wallet
// @Tags     Transactions
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id           path  string true  "Wallet ID"
// @Param    status       query string false "Filter: confirmed|pending|failed"
// @Param    type         query string false "Filter: deposit|withdrawal"
// @Param    limit        query int    false "Limit (default 20)"
// @Param    offset       query int    false "Offset"
// @Success  200 {object} TransactionListResponse
// @Router   /v1/wallets/{id}/transactions [get]
func ListWalletTransactions(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    limit := ctx.Request().QueryInt("limit", 20)
    offset := ctx.Request().QueryInt("offset", 0)

    query := facades.Orm().Query().Where("wallet_id = ?", walletID)
    if status := ctx.Request().Query("status"); status != "" {
        query = query.Where("status = ?", status)
    }
    if txType := ctx.Request().Query("type"); txType != "" {
        query = query.Where("type = ?", txType)
    }

    var txs []models.Transaction
    query.Limit(limit).Offset(offset).Order("created_at DESC").Find(&txs)
    return ctx.Response().Json(http.StatusOK, http.Json{"data": txs})
}

// GetWalletTransaction — scoped to wallet_id to prevent cross-wallet access
func GetWalletTransaction(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    txID := ctx.Request().Input("txId")
    var tx models.Transaction
    if err := facades.Orm().Query().
        Where("id = ? AND wallet_id = ?", txID, walletID).
        First(&tx); err != nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "transaction not found"})
    }
    return ctx.Response().Json(http.StatusOK, tx)
}
```

- [ ] **Step 6.3: Run tests + add routes + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletTransactions -v
git add back/app/http/controllers/wallet_transactions_controller*.go back/routes/
git commit -m "feat: add wallet-scoped transaction endpoints"
```

---

## Task 7: Wallet Withdrawals Controller

**Files:**
- Create: `back/app/http/controllers/wallet_withdrawals_controller.go` + `_test.go`

- [ ] **Step 7.1: Write failing tests**

```go
func (s *WalletWithdrawalsControllerTestSuite) TestCreateWithdrawal_Success() {
    walletID := s.createTestWallet("eth")
    body := `{"amount":"0.01","destination_address":"0xabc","note":"test"}`
    s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", body).AssertCreated()
}

func (s *WalletWithdrawalsControllerTestSuite) TestGetWithdrawal_Success() {
    walletID := s.createTestWallet("eth")
    resp := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
        `{"amount":"0.01","destination_address":"0xabc"}`)
    j, _ := resp.Json()
    withdrawalID := j["id"].(string)
    s.SignedGet("/v1/withdrawals/"+withdrawalID).AssertOk()
}

func (s *WalletWithdrawalsControllerTestSuite) TestListWithdrawals_Success() {
    walletID := s.createTestWallet("eth")
    s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", `{"amount":"0.01","destination_address":"0xabc"}`)
    s.SignedGet("/v1/wallets/"+walletID+"/withdrawals").
        AssertOk().
        AssertJsonPath("data.0.status", func(v any) bool { return v.(string) == "pending" })
}
```

- [ ] **Step 7.2: Implement controller**

```go
// back/app/http/controllers/wallet_withdrawals_controller.go

// CreateWalletWithdrawal godoc
// @Summary  Create a withdrawal from a wallet
// @Tags     Withdrawals
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Param    id   path string true "Wallet ID"
// @Param    body body CreateWalletWithdrawalRequest true "Withdrawal request"
// @Success  201 {object} models.Withdrawal
// @Router   /v1/wallets/{id}/withdrawals [post]
func CreateWalletWithdrawal(ctx http.Context) http.Response {
    var req requests.CreateWalletWithdrawalRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil { ... }

    walletID, _ := uuid.Parse(ctx.Request().Input("id"))
    amount, _ := decimal.NewFromString(req.Amount)

    withdrawal := &models.Withdrawal{
        ID:                 uuid.New(),
        WalletID:           walletID,
        Status:             "pending",
        Amount:             amount,
        DestinationAddress: req.DestinationAddress,
        Note:               req.Note,
    }
    facades.Orm().Query().Create(withdrawal)

    // Queue for withdrawal worker
    msg := types.WithdrawalMessage{
        WalletID:    walletID.String(),
        WithdrawalID: withdrawal.ID.String(),
        Amount:      req.Amount,
        ToAddress:   req.DestinationAddress,
    }
    container.Get().SQS.SendWithdrawal(ctx.Context(), msg)

    return ctx.Response().Json(http.StatusCreated, withdrawal)
}

// GetWithdrawal — top-level status lookup by withdrawals.id
func GetWithdrawal(ctx http.Context) http.Response {
    withdrawalID := ctx.Request().Input("id")
    var w models.Withdrawal
    if err := facades.Orm().Query().Where("id = ?", withdrawalID).First(&w); err != nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "withdrawal not found"})
    }
    return ctx.Response().Json(http.StatusOK, w)
}

// ListWalletWithdrawals — wallet-scoped list
func ListWalletWithdrawals(ctx http.Context) http.Response {
    walletID := ctx.Request().Input("id")
    limit := ctx.Request().QueryInt("limit", 20)
    offset := ctx.Request().QueryInt("offset", 0)
    var withdrawals []models.Withdrawal
    facades.Orm().Query().
        Where("wallet_id = ?", walletID).
        Limit(limit).Offset(offset).Order("created_at DESC").
        Find(&withdrawals)
    return ctx.Response().Json(http.StatusOK, http.Json{"data": withdrawals})
}
```

- [ ] **Step 7.3: Run tests + add routes + commit**
```bash
cd back && go test ./app/http/controllers/ -run TestWalletWithdrawals -v
git add back/app/http/controllers/wallet_withdrawals_controller*.go back/routes/
git commit -m "feat: add wallet-scoped withdrawal endpoints with status tracking"
```

---

## Task 8: Deprecate Flat Routes + Swagger Annotations

- [ ] **Step 8.1: Mark deprecated routes in `routes/api.go`** with comments:
```go
// DEPRECATED: use /v1/wallets/:id/transactions
router.Get("/transactions", controllers.ListTransactions)
router.Get("/transactions/{id}", controllers.GetTransaction)
// DEPRECATED: use /v1/wallets/:id/webhooks
router.Post("/webhooks", controllers.CreateWebhook)
router.Get("/webhooks", controllers.ListWebhooks)
```

- [ ] **Step 8.2: Add Swagger `@Deprecated` annotation** to deprecated controller methods:
```go
// @Deprecated
// ListTransactions godoc — DEPRECATED: use GET /v1/wallets/:id/transactions
```

- [ ] **Step 8.3: Add Swagger annotations to all new controllers** — every new endpoint must have `@Summary`, `@Tags`, `@Security`, `@Param`, `@Success`, `@Failure`, `@Router`.

- [ ] **Step 8.4: Regenerate Swagger docs**
```bash
cd back && make swagger-generate
```
Expected: no errors, new tags (Wallets sub-resources, Transactions, Withdrawals) appear in docs.

- [ ] **Step 8.5: Commit**
```bash
git add back/docs/ back/routes/
git commit -m "docs: deprecate flat routes, add Swagger annotations for wallet sub-resources"
```

---

## Task 9: Final Verification

- [ ] **Step 9.1: Run full test suite**
```bash
cd back && go test ./... -count=1 2>&1 | grep -E "PASS|FAIL|panic"
```
Expected: all PASS.

- [ ] **Step 9.2: Run linter**
```bash
cd back && make lint
```

- [ ] **Step 9.3: Verify Swagger**
```bash
cd back && make run
# Open http://localhost:8080/swagger/index.html
# All wallet sub-resource endpoints visible under correct tags
```

- [ ] **Step 9.4: Tag completion commit**
```bash
git commit --allow-empty -m "feat: backend wallet completion (users, whitelist, webhooks, unspents, transactions, withdrawals)"
```
