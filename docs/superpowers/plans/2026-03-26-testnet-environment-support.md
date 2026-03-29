# Testnet Environment Support — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable per-account testnet/mainnet support so users can create wallets and transact on both environments, with a visible topbar toggle and database-driven chain/token management.

**Architecture:** Paired accounts (prod ↔ test) share memberships but isolate data. Chains/tokens move from hardcoded Go to DB tables loaded at boot. A new `AccountHeader` middleware reads `X-Account-Id` and enforces environment boundaries on all session-auth routes. The frontend toggle swaps accounts and the UI changes to amber when on testnet.

**Tech Stack:** Go (Goravel), PostgreSQL, Next.js (Pages Router), Redux Toolkit, SWR, Tailwind CSS

**Spec:** `docs/superpowers/specs/2026-03-26-testnet-environment-support-design.md`

---

## Phase 1: Database Migrations & Models

### Task 1: Create `chains` table

**Files:**
- Create: `back/database/migrations/20260326000001_create_chains_table.go`
- Create: `back/app/models/chain.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write the migration file**

Create `back/database/migrations/20260326000001_create_chains_table.go`:

```go
package migrations

import "github.com/goravel/framework/contracts/database/schema"

type M20260326000001CreateChainsTable struct{}

func (m *M20260326000001CreateChainsTable) Signature() string {
    return "20260326000001_create_chains_table"
}

func (m *M20260326000001CreateChainsTable) Up(s schema.Schema) error {
    return s.Create("chains", func(table schema.Blueprint) {
        table.String("id", 20)
        table.Primary("id")
        table.String("name", 100)
        table.String("adapter_type", 20) // evm, bitcoin, solana
        table.String("native_symbol", 20)
        table.Integer("native_decimals")
        table.BigInteger("network_id").Nullable()
        table.Text("rpc_url") // encrypted via facades.Crypt()
        table.Boolean("is_testnet").Default(false)
        table.String("mainnet_chain_id", 20).Nullable()
        table.Integer("required_confirmations")
        table.String("icon_url", 500).Nullable()
        table.Integer("display_order").Default(0)
        table.String("status", 20).Default("active")
        table.Timestamps()

        table.Foreign("mainnet_chain_id").References("id").On("chains").NullOnDelete()
    })
}

func (m *M20260326000001CreateChainsTable) Down(s schema.Schema) error {
    return s.DropIfExists("chains")
}
```

- [ ] **Step 2: Write the Chain model**

Create `back/app/models/chain.go`:

```go
package models

import "github.com/goravel/framework/database/orm"

const (
    AdapterTypeEVM     = "evm"
    AdapterTypeBitcoin = "bitcoin"
    AdapterTypeSolana  = "solana"

    EnvironmentProd = "prod"
    EnvironmentTest = "test"
)

type Chain struct {
    orm.Model
    ID                    string  `gorm:"type:varchar(20);primary_key" json:"id"`
    Name                  string  `gorm:"type:varchar(100);not null" json:"name"`
    AdapterType           string  `gorm:"type:varchar(20);not null" json:"adapter_type"`
    NativeSymbol          string  `gorm:"type:varchar(20);not null" json:"native_symbol"`
    NativeDecimals        int     `gorm:"not null" json:"native_decimals"`
    NetworkID             *int64  `gorm:"type:bigint" json:"network_id,omitempty"`
    RpcURL                string  `gorm:"type:text;not null" json:"-"`
    IsTestnet             bool    `gorm:"default:false" json:"is_testnet"`
    MainnetChainID        *string `gorm:"type:varchar(20)" json:"mainnet_chain_id,omitempty"`
    RequiredConfirmations int     `gorm:"not null" json:"required_confirmations"`
    IconURL               *string `gorm:"type:varchar(500)" json:"icon_url,omitempty"`
    DisplayOrder          int     `gorm:"default:0" json:"display_order"`
    Status                string  `gorm:"type:varchar(20);default:active" json:"status"`
}

func (c *Chain) TableName() string { return "chains" }
```

- [ ] **Step 3: Register migration**

In `back/database/migrations/migrations.go`, add to the `All()` slice:

```go
&M20260326000001CreateChainsTable{},
```

- [ ] **Step 4: Run migration and verify**

Run: `cd back && go run . artisan migrate`
Expected: Table `chains` created successfully.

- [ ] **Step 5: Commit**

```bash
git add back/database/migrations/20260326000001_create_chains_table.go back/app/models/chain.go back/database/migrations/migrations.go
git commit -m "feat: create chains table and model"
```

---

### Task 2: Create `tokens` table

**Files:**
- Create: `back/database/migrations/20260326000002_create_tokens_table.go`
- Create: `back/app/models/token.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write the migration file**

Create `back/database/migrations/20260326000002_create_tokens_table.go`:

```go
package migrations

import "github.com/goravel/framework/contracts/database/schema"

type M20260326000002CreateTokensTable struct{}

func (m *M20260326000002CreateTokensTable) Signature() string {
    return "20260326000002_create_tokens_table"
}

func (m *M20260326000002CreateTokensTable) Up(s schema.Schema) error {
    return s.Create("tokens", func(table schema.Blueprint) {
        table.UuidMorphs("ID")
        table.String("chain_id", 20)
        table.String("symbol", 20)
        table.String("name", 100)
        table.String("contract_address", 255)
        table.Integer("decimals")
        table.String("icon_url", 500).Nullable()
        table.String("status", 20).Default("active")
        table.Timestamps()

        table.Foreign("chain_id").References("id").On("chains").CascadeOnDelete()
    })
}

func (m *M20260326000002CreateTokensTable) Down(s schema.Schema) error {
    return s.DropIfExists("tokens")
}
```

- [ ] **Step 2: Write the Token model**

Create `back/app/models/token.go`:

```go
package models

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/database/orm"
)

type Token struct {
    orm.Model
    ID              uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    ChainID         string    `gorm:"type:varchar(20);not null" json:"chain_id"`
    Symbol          string    `gorm:"type:varchar(20);not null" json:"symbol"`
    Name            string    `gorm:"type:varchar(100);not null" json:"name"`
    ContractAddress string    `gorm:"type:varchar(255);not null" json:"contract_address"`
    Decimals        int       `gorm:"not null" json:"decimals"`
    IconURL         *string   `gorm:"type:varchar(500)" json:"icon_url,omitempty"`
    Status          string    `gorm:"type:varchar(20);default:active" json:"status"`
}

func (t *Token) TableName() string { return "tokens" }
```

- [ ] **Step 3: Register migration, run, and commit**

Add `&M20260326000002CreateTokensTable{}` to `migrations.go`.
Run: `cd back && go run . artisan migrate`

```bash
git add -A && git commit -m "feat: create tokens table and model"
```

---

### Task 3: Create `chain_resources` table

**Files:**
- Create: `back/database/migrations/20260326000003_create_chain_resources_table.go`
- Create: `back/app/models/chain_resource.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write migration + model**

Create `back/database/migrations/20260326000003_create_chain_resources_table.go`:

```go
package migrations

import "github.com/goravel/framework/contracts/database/schema"

type M20260326000003CreateChainResourcesTable struct{}

func (m *M20260326000003CreateChainResourcesTable) Signature() string {
    return "20260326000003_create_chain_resources_table"
}

func (m *M20260326000003CreateChainResourcesTable) Up(s schema.Schema) error {
    return s.Create("chain_resources", func(table schema.Blueprint) {
        table.UuidMorphs("ID")
        table.String("chain_id", 20)
        table.String("type", 20) // faucet, explorer, bridge, docs
        table.String("name", 100)
        table.String("url", 500)
        table.Text("description").Nullable()
        table.Integer("display_order").Default(0)
        table.String("status", 20).Default("active")
        table.Timestamps()

        table.Foreign("chain_id").References("id").On("chains").CascadeOnDelete()
    })
}

func (m *M20260326000003CreateChainResourcesTable) Down(s schema.Schema) error {
    return s.DropIfExists("chain_resources")
}
```

Create `back/app/models/chain_resource.go`:

```go
package models

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/database/orm"
)

type ChainResource struct {
    orm.Model
    ID           uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    ChainID      string    `gorm:"type:varchar(20);not null" json:"chain_id"`
    Type         string    `gorm:"type:varchar(20);not null" json:"type"`
    Name         string    `gorm:"type:varchar(100);not null" json:"name"`
    URL          string    `gorm:"type:varchar(500);not null" json:"url"`
    Description  *string   `gorm:"type:text" json:"description,omitempty"`
    DisplayOrder int       `gorm:"default:0" json:"display_order"`
    Status       string    `gorm:"type:varchar(20);default:active" json:"status"`
}

func (cr *ChainResource) TableName() string { return "chain_resources" }
```

- [ ] **Step 2: Register migration, run, and commit**

```bash
git add -A && git commit -m "feat: create chain_resources table and model"
```

---

### Task 4: Alter `accounts` table — add `environment` and `linked_account_id`

**Files:**
- Create: `back/database/migrations/20260326000004_alter_accounts_add_environment.go`
- Modify: `back/app/models/account.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write migration**

Create `back/database/migrations/20260326000004_alter_accounts_add_environment.go`:

```go
package migrations

import "github.com/goravel/framework/contracts/database/schema"

type M20260326000004AlterAccountsAddEnvironment struct{}

func (m *M20260326000004AlterAccountsAddEnvironment) Signature() string {
    return "20260326000004_alter_accounts_add_environment"
}

func (m *M20260326000004AlterAccountsAddEnvironment) Up(s schema.Schema) error {
    return s.Table("accounts", func(table schema.Blueprint) {
        table.String("environment", 4).Default("prod")
        table.Uuid("linked_account_id").Nullable()

        table.Foreign("linked_account_id").References("id").On("accounts").NullOnDelete()
    })
}

func (m *M20260326000004AlterAccountsAddEnvironment) Down(s schema.Schema) error {
    return s.Table("accounts", func(table schema.Blueprint) {
        table.DropColumn("environment", "linked_account_id")
    })
}
```

- [ ] **Step 2: Update Account model**

In `back/app/models/account.go`, add the new fields:

```go
type Account struct {
    orm.Model
    ID               uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
    Name             string     `gorm:"type:varchar(255);not null" json:"name"`
    Status           string     `gorm:"type:varchar(20);default:active" json:"status"`
    ViewAllWallets   bool       `gorm:"default:false" json:"view_all_wallets"`
    Environment      string     `gorm:"type:varchar(4);default:prod" json:"environment"`
    LinkedAccountID  *uuid.UUID `gorm:"type:uuid" json:"linked_account_id,omitempty"`
}
```

- [ ] **Step 3: Register migration, run, and commit**

```bash
git add -A && git commit -m "feat: add environment and linked_account_id to accounts"
```

---

### Task 5: Alter `users` table — add `default_account_id`

**Files:**
- Create: `back/database/migrations/20260326000005_alter_users_add_default_account.go`
- Modify: `back/app/models/user.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write migration**

Create `back/database/migrations/20260326000005_alter_users_add_default_account.go`:

```go
package migrations

import "github.com/goravel/framework/contracts/database/schema"

type M20260326000005AlterUsersAddDefaultAccount struct{}

func (m *M20260326000005AlterUsersAddDefaultAccount) Signature() string {
    return "20260326000005_alter_users_add_default_account"
}

func (m *M20260326000005AlterUsersAddDefaultAccount) Up(s schema.Schema) error {
    return s.Table("users", func(table schema.Blueprint) {
        table.Uuid("default_account_id").Nullable()
        table.Foreign("default_account_id").References("id").On("accounts").NullOnDelete()
    })
}

func (m *M20260326000005AlterUsersAddDefaultAccount) Down(s schema.Schema) error {
    return s.Table("users", func(table schema.Blueprint) {
        table.DropColumn("default_account_id")
    })
}
```

- [ ] **Step 2: Update User model**

In `back/app/models/user.go`, add:

```go
DefaultAccountID *uuid.UUID `gorm:"type:uuid" json:"default_account_id,omitempty"`
```

- [ ] **Step 3: Register migration, run, and commit**

```bash
git add -A && git commit -m "feat: add default_account_id to users"
```

---

## Phase 2: Repositories & DB-Driven Boot

### Task 6: Chain, Token, ChainResource repositories

**Files:**
- Create: `back/app/repositories/chain_repository.go`
- Create: `back/app/repositories/token_repository.go`
- Create: `back/app/repositories/chain_resource_repository.go`
- Modify: `back/app/container/container.go` (register repos)

- [ ] **Step 1: Write ChainRepository**

Create `back/app/repositories/chain_repository.go`:

```go
package repositories

import (
    "github.com/goravel/framework/facades"
    "github.com/macrowallets/waas/app/models"
)

type ChainRepository interface {
    FindAll() ([]models.Chain, error)
    FindActive() ([]models.Chain, error)
    FindByID(id string) (*models.Chain, error)
    FindByTestnet(isTestnet bool) ([]models.Chain, error)
    Create(chain *models.Chain) error
}

type chainRepository struct{}

func NewChainRepository() ChainRepository { return &chainRepository{} }

func (r *chainRepository) FindAll() ([]models.Chain, error) {
    var chains []models.Chain
    err := facades.Orm().Query().Order("display_order ASC").Find(&chains)
    return chains, err
}

func (r *chainRepository) FindActive() ([]models.Chain, error) {
    var chains []models.Chain
    err := facades.Orm().Query().Where("status", "active").Order("display_order ASC").Find(&chains)
    return chains, err
}

func (r *chainRepository) FindByID(id string) (*models.Chain, error) {
    var chain models.Chain
    err := facades.Orm().Query().Where("id", id).First(&chain)
    if chain.ID == "" {
        return nil, err
    }
    return &chain, err
}

func (r *chainRepository) FindByTestnet(isTestnet bool) ([]models.Chain, error) {
    var chains []models.Chain
    err := facades.Orm().Query().Where("status", "active").Where("is_testnet", isTestnet).Order("display_order ASC").Find(&chains)
    return chains, err
}

func (r *chainRepository) Create(chain *models.Chain) error {
    return facades.Orm().Query().Create(chain)
}
```

- [ ] **Step 2: Write TokenRepository**

Create `back/app/repositories/token_repository.go`:

```go
package repositories

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/facades"
    "github.com/macrowallets/waas/app/models"
)

type TokenRepository interface {
    FindByChainID(chainID string) ([]models.Token, error)
    FindActive() ([]models.Token, error)
    FindByID(id uuid.UUID) (*models.Token, error)
    Create(token *models.Token) error
}

type tokenRepository struct{}

func NewTokenRepository() TokenRepository { return &tokenRepository{} }

func (r *tokenRepository) FindByChainID(chainID string) ([]models.Token, error) {
    var tokens []models.Token
    err := facades.Orm().Query().Where("chain_id", chainID).Where("status", "active").Find(&tokens)
    return tokens, err
}

func (r *tokenRepository) FindActive() ([]models.Token, error) {
    var tokens []models.Token
    err := facades.Orm().Query().Where("status", "active").Find(&tokens)
    return tokens, err
}

func (r *tokenRepository) FindByID(id uuid.UUID) (*models.Token, error) {
    var token models.Token
    err := facades.Orm().Query().Where("id", id).First(&token)
    if token.ID == uuid.Nil {
        return nil, err
    }
    return &token, err
}

func (r *tokenRepository) Create(token *models.Token) error {
    return facades.Orm().Query().Create(token)
}
```

- [ ] **Step 3: Write ChainResourceRepository**

Create `back/app/repositories/chain_resource_repository.go`:

```go
package repositories

import (
    "github.com/goravel/framework/facades"
    "github.com/macrowallets/waas/app/models"
)

type ChainResourceRepository interface {
    FindByChainID(chainID string) ([]models.ChainResource, error)
    FindByChainAndType(chainID, resourceType string) ([]models.ChainResource, error)
    Create(resource *models.ChainResource) error
}

type chainResourceRepository struct{}

func NewChainResourceRepository() ChainResourceRepository { return &chainResourceRepository{} }

func (r *chainResourceRepository) FindByChainID(chainID string) ([]models.ChainResource, error) {
    var resources []models.ChainResource
    err := facades.Orm().Query().Where("chain_id", chainID).Where("status", "active").Order("display_order ASC").Find(&resources)
    return resources, err
}

func (r *chainResourceRepository) FindByChainAndType(chainID, resourceType string) ([]models.ChainResource, error) {
    var resources []models.ChainResource
    err := facades.Orm().Query().Where("chain_id", chainID).Where("type", resourceType).Where("status", "active").Order("display_order ASC").Find(&resources)
    return resources, err
}

func (r *chainResourceRepository) Create(resource *models.ChainResource) error {
    return facades.Orm().Query().Create(resource)
}
```

- [ ] **Step 4: Register repos in container**

In `back/app/container/container.go`, add to the Container struct:

```go
ChainRepo         repositories.ChainRepository
TokenRepo         repositories.TokenRepository
ChainResourceRepo repositories.ChainResourceRepository
```

In `Boot()`, add after existing repository initialization:

```go
c.ChainRepo = repositories.NewChainRepository()
c.TokenRepo = repositories.NewTokenRepository()
c.ChainResourceRepo = repositories.NewChainResourceRepository()
```

- [ ] **Step 5: Compile and commit**

Run: `cd back && go build ./...`

```bash
git add -A && git commit -m "feat: add chain, token, chain_resource repositories"
```

---

### Task 7: Refactor container boot to load chains from DB

**Files:**
- Modify: `back/app/container/container.go` (replace hardcoded chain registrations)
- Modify: `back/app/services/chain/registry.go` (remove `AllTokens()`)

- [ ] **Step 1: Replace hardcoded chain registration with DB-driven boot**

In `back/app/container/container.go`, replace the hardcoded chain registration block (the `c.Registry.RegisterChain(...)` and token loop) with:

```go
// --- Chain Registry (DB-driven) ---
c.Registry = chainpkg.NewRegistry()
activeChains, err := c.ChainRepo.FindActive()
if err != nil {
    slog.Error("failed to load chains from DB", "error", err)
} else {
    for _, ch := range activeChains {
        rpcURL, decErr := facades.Crypt().DecryptString(ch.RpcURL)
        if decErr != nil {
            slog.Warn("failed to decrypt RPC URL, skipping chain", "chain", ch.ID, "error", decErr)
            continue
        }
        if rpcURL == "" {
            slog.Warn("empty RPC URL, skipping chain", "chain", ch.ID)
            continue
        }
        var adapter types.Chain
        switch ch.AdapterType {
        case models.AdapterTypeEVM:
            networkID := int64(0)
            if ch.NetworkID != nil {
                networkID = *ch.NetworkID
            }
            adapter = chainpkg.NewEVMLive(chainpkg.EVMConfig{
                ChainIDStr:    ch.ID,
                ChainName:     ch.Name,
                NativeSymbol:  ch.NativeSymbol,
                NativeDecimal: uint8(ch.NativeDecimals),
                NetworkID:     networkID,
                RPCURL:        rpcURL,
                Confirmations: uint64(ch.RequiredConfirmations),
            })
        case models.AdapterTypeBitcoin:
            network := "mainnet"
            if ch.IsTestnet {
                network = "testnet"
            }
            adapter = chainpkg.NewBitcoinLive(chainpkg.BitcoinConfig{
                RPCURL:  rpcURL,
                Network: network,
            })
        case models.AdapterTypeSolana:
            adapter = chainpkg.NewSolanaLive(rpcURL)
        default:
            slog.Warn("unknown adapter type, skipping", "chain", ch.ID, "adapter", ch.AdapterType)
            continue
        }
        c.Registry.RegisterChain(adapter)
    }
}

// Load tokens from DB
activeTokens, err := c.TokenRepo.FindActive()
if err != nil {
    slog.Error("failed to load tokens from DB", "error", err)
} else {
    for _, t := range activeTokens {
        c.Registry.RegisterToken(types.Token{
            Symbol:   t.Symbol,
            Name:     t.Name,
            Contract: t.ContractAddress,
            Decimals: uint8(t.Decimals),
            ChainID:  t.ChainID,
        })
    }
}
```

Remove the old hardcoded ETH, Polygon, Solana, Bitcoin registrations and the `AllTokens()` loop.

- [ ] **Step 2: Remove `AllTokens()` from registry.go**

In `back/app/services/chain/registry.go`, delete the `AllTokens()` function entirely. Tokens now come from the DB.

- [ ] **Step 3: Update imports in container.go**

Add `"github.com/goravel/framework/facades"` if not already imported. Add `"github.com/macrowallets/waas/app/models"` for the adapter type constants. Ensure `types` import points to `"github.com/macrowallets/waas/pkg/types"`.

- [ ] **Step 4: Compile and verify**

Run: `cd back && go build ./...`
Expected: Clean compile. The server won't start fully until chains are seeded.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: load chains and tokens from database at boot"
```

---

## Phase 3: Middleware & Auth

### Task 8: AccountHeader middleware

**Files:**
- Create: `back/app/http/middleware/account_header.go`

- [ ] **Step 1: Write the middleware**

Create `back/app/http/middleware/account_header.go`:

```go
package middleware

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/contracts/http"

    "github.com/macrowallets/waas/app/container"
)

func AccountHeader(ctx http.Context) {
    rawID := ctx.Request().Header("X-Account-Id")
    if rawID == "" {
        ctx.Request().AbortWithStatus(http.StatusBadRequest)
        ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "X-Account-Id header is required"})
        return
    }

    accountID, err := uuid.Parse(rawID)
    if err != nil {
        ctx.Request().AbortWithStatus(http.StatusBadRequest)
        ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid X-Account-Id"})
        return
    }

    accountPtr, err := container.Get().AccountRepo.FindByID(accountID)
    if err != nil || accountPtr == nil {
        ctx.Request().AbortWithStatus(http.StatusNotFound)
        ctx.Response().Json(http.StatusNotFound, http.Json{"error": "account not found"})
        return
    }

    userID := contextUserID(ctx)
    if userID == uuid.Nil {
        ctx.Request().AbortWithStatus(http.StatusUnauthorized)
        ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
        return
    }

    au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
    if err != nil || au == nil {
        ctx.Request().AbortWithStatus(http.StatusForbidden)
        ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
        return
    }

    ctx.WithValue("account", accountPtr)
    ctx.WithValue("account_id", accountID)
    ctx.WithValue("account_role", au.Role)
    ctx.WithValue("account_environment", accountPtr.Environment)
    ctx.Request().Next()
}
```

Note: `contextUserID` is already defined in `back/app/http/middleware/helpers.go` (or wherever the existing `AccountContext` middleware gets it from). Verify the exact file.

- [ ] **Step 2: Compile and commit**

Run: `cd back && go build ./...`

```bash
git add -A && git commit -m "feat: add AccountHeader middleware for X-Account-Id"
```

---

### Task 9: Wire AccountHeader to session-auth routes

**Files:**
- Modify: `back/routes/api.go`

- [ ] **Step 1: Add AccountHeader to wallet and chain routes**

In `back/routes/api.go`, update the wallet routes group to include `middleware.AccountHeader`:

Change:
```go
facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth, noCache).Group(func(router route.Router) {
```

To:
```go
facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth, middleware.AccountHeader, noCache).Group(func(router route.Router) {
```

- [ ] **Step 2: Add chains routes for session-auth**

Add a new route group before the external API block:

```go
// Chain info — session auth + account header
facades.Route().Prefix("/v1/chains").Middleware(middleware.SessionAuth, middleware.AccountHeader, noCache).Group(func(router route.Router) {
    router.Get("", controllers.ListChains)
    router.Get("/{chainId}", controllers.GetChain)
    router.Get("/{chainId}/tokens", controllers.ListChainTokens)
    router.Get("/{chainId}/resources", controllers.ListChainResources)
})
```

- [ ] **Step 3: Add default-account endpoint under /v1/users**

In the existing `/v1/users` route group, add:

```go
router.Patch("/me/default-account", controllers.UpdateDefaultAccount)
```

- [ ] **Step 4: Compile and commit**

```bash
git add -A && git commit -m "feat: wire AccountHeader middleware to session routes"
```

---

### Task 10: Update Login to return account data

**Files:**
- Modify: `back/app/http/controllers/auth_controller.go`

- [ ] **Step 1: Update Login handler to include account info**

After the JWT + refresh token creation in `Login()`, before the return, add account resolution:

```go
// Resolve default account
var accountResponse interface{}
var accountsResponse interface{}
var defaultAccountID string

memberships, _ := container.Get().AccountUserRepo.FindByUserID(user.ID)
if len(memberships) > 0 {
    accounts := make([]map[string]interface{}, 0, len(memberships))
    for _, m := range memberships {
        acct, _ := container.Get().AccountRepo.FindByID(m.AccountID)
        if acct != nil {
            accounts = append(accounts, map[string]interface{}{
                "id":                acct.ID,
                "name":             acct.Name,
                "environment":      acct.Environment,
                "linked_account_id": acct.LinkedAccountID,
                "status":           acct.Status,
                "role":             m.Role,
            })
        }
    }
    accountsResponse = accounts

    // Pick default account
    if user.DefaultAccountID != nil {
        defaultAccountID = user.DefaultAccountID.String()
        for _, a := range accounts {
            if a["id"].(uuid.UUID).String() == defaultAccountID {
                accountResponse = a
                break
            }
        }
    }
    if accountResponse == nil && len(accounts) > 0 {
        accountResponse = accounts[0]
        defaultAccountID = accounts[0]["id"].(uuid.UUID).String()
    }
}
```

Update the return JSON to include:
```go
return ctx.Response().Json(http.StatusOK, http.Json{
    "access_token":  accessToken,
    "refresh_token": rawRefresh,
    "user":          user,
    "account_id":    defaultAccountID,
    "account":       accountResponse,
    "accounts":      accountsResponse,
})
```

- [ ] **Step 2: Compile and commit**

```bash
git add -A && git commit -m "feat: login returns account data with environment info"
```

---

### Task 11: Update Registration to create paired accounts

**Files:**
- Modify: `back/app/http/controllers/auth_controller.go`

- [ ] **Step 1: Add organization_name to RegisterRequest**

```go
type RegisterRequest struct {
    Email            string `json:"email" example:"user@example.com"`
    Password         string `json:"password" example:"s3cr3t"`
    FullName         string `json:"full_name" example:"Alice Smith"`
    OrganizationName string `json:"organization_name" example:"Acme Corp"`
}
```

- [ ] **Step 2: Update Register handler to create paired accounts**

After user creation, before the welcome email, add:

```go
if req.OrganizationName == "" {
    return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "organization_name is required"})
}

// Create paired prod + test accounts in a single transaction
prodAccount := &models.Account{
    ID:          uuid.New(),
    Name:        req.OrganizationName,
    Status:      "active",
    Environment: models.EnvironmentProd,
}
testAccount := &models.Account{
    ID:          uuid.New(),
    Name:        req.OrganizationName,
    Status:      "active",
    Environment: models.EnvironmentTest,
}
prodAccount.LinkedAccountID = &testAccount.ID
testAccount.LinkedAccountID = &prodAccount.ID

if err := container.Get().AccountRepo.Create(prodAccount); err != nil {
    return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create organization"})
}
if err := container.Get().AccountRepo.Create(testAccount); err != nil {
    return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create test organization"})
}

// Add user as owner of both
for _, acctID := range []uuid.UUID{prodAccount.ID, testAccount.ID} {
    au := &models.AccountUser{
        ID:        uuid.New(),
        AccountID: acctID,
        UserID:    user.ID,
        Role:      "owner",
        Status:    "active",
    }
    _ = container.Get().AccountUserRepo.Create(au)
}

// Set default account to prod
user.DefaultAccountID = &prodAccount.ID
_ = container.Get().UserRepo.Update(user)
```

Update the return to include account info (same pattern as Login).

- [ ] **Step 3: Compile and commit**

```bash
git add -A && git commit -m "feat: registration creates paired prod+test accounts"
```

---

### Task 12: Default account endpoint

**Files:**
- Modify: `back/app/http/controllers/user_controller.go` (or create a new one)

- [ ] **Step 1: Add UpdateDefaultAccount handler**

Add to `back/app/http/controllers/user_controller.go`:

```go
func UpdateDefaultAccount(ctx http.Context) http.Response {
    userID := ctx.Value("user_id").(uuid.UUID)

    var req struct {
        AccountID string `json:"account_id"`
    }
    if err := ctx.Request().Bind(&req); err != nil || req.AccountID == "" {
        return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "account_id is required"})
    }

    accountID, err := uuid.Parse(req.AccountID)
    if err != nil {
        return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid account_id"})
    }

    // Verify membership
    au, err := container.Get().AccountUserRepo.FindByAccountAndUser(accountID, userID)
    if err != nil || au == nil {
        return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
    }

    // Update default
    userPtr, _ := container.Get().UserRepo.FindByID(userID)
    if userPtr == nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "user not found"})
    }
    userPtr.DefaultAccountID = &accountID
    if err := container.Get().UserRepo.Update(userPtr); err != nil {
        return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update default account"})
    }

    account, _ := container.Get().AccountRepo.FindByID(accountID)
    return ctx.Response().Json(http.StatusOK, http.Json{
        "account": account,
    })
}
```

- [ ] **Step 2: Compile and commit**

```bash
git add -A && git commit -m "feat: add PATCH /v1/users/me/default-account endpoint"
```

---

## Phase 4: Environment-Filtered Endpoints

### Task 13: Environment-filtered chain endpoints

**Files:**
- Modify: `back/app/http/controllers/chains_controller.go`

- [ ] **Step 1: Rewrite ListChains with environment filtering**

Replace the existing `ListChains` function:

```go
func ListChains(ctx http.Context) http.Response {
    env, _ := ctx.Value("account_environment").(string)
    isTestnet := env == models.EnvironmentTest

    chains, err := container.Get().ChainRepo.FindByTestnet(isTestnet)
    if err != nil {
        return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to load chains"})
    }

    type chainInfo struct {
        ID            string                 `json:"id"`
        Name          string                 `json:"name"`
        NativeAsset   string                 `json:"native_asset"`
        NativeDecimals int                   `json:"native_decimals"`
        NetworkID     *int64                 `json:"network_id,omitempty"`
        IsTestnet     bool                   `json:"is_testnet"`
        Confirmations int                    `json:"required_confirmations"`
        IconURL       *string                `json:"icon_url,omitempty"`
        Tokens        []models.Token         `json:"tokens"`
        Resources     []models.ChainResource `json:"resources"`
    }

    var result []chainInfo
    for _, c := range chains {
        tokens, _ := container.Get().TokenRepo.FindByChainID(c.ID)
        resources, _ := container.Get().ChainResourceRepo.FindByChainID(c.ID)
        result = append(result, chainInfo{
            ID:            c.ID,
            Name:          c.Name,
            NativeAsset:   c.NativeSymbol,
            NativeDecimals: c.NativeDecimals,
            NetworkID:     c.NetworkID,
            IsTestnet:     c.IsTestnet,
            Confirmations: c.RequiredConfirmations,
            IconURL:       c.IconURL,
            Tokens:        tokens,
            Resources:     resources,
        })
    }

    return ctx.Response().Success().Json(http.Json{"data": result})
}
```

- [ ] **Step 2: Add GetChain, ListChainTokens, ListChainResources handlers**

Add to the same file:

```go
func GetChain(ctx http.Context) http.Response {
    chainID := ctx.Request().Input("chainId")
    chain, err := container.Get().ChainRepo.FindByID(chainID)
    if err != nil || chain == nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "chain not found"})
    }

    env, _ := ctx.Value("account_environment").(string)
    isTestnet := env == models.EnvironmentTest
    if chain.IsTestnet != isTestnet {
        return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
    }

    tokens, _ := container.Get().TokenRepo.FindByChainID(chain.ID)
    resources, _ := container.Get().ChainResourceRepo.FindByChainID(chain.ID)

    return ctx.Response().Success().Json(http.Json{
        "data": map[string]interface{}{
            "chain":     chain,
            "tokens":    tokens,
            "resources": resources,
        },
    })
}

func ListChainTokens(ctx http.Context) http.Response {
    chainID := ctx.Request().Input("chainId")
    tokens, err := container.Get().TokenRepo.FindByChainID(chainID)
    if err != nil {
        return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to load tokens"})
    }
    return ctx.Response().Success().Json(http.Json{"data": tokens})
}

func ListChainResources(ctx http.Context) http.Response {
    chainID := ctx.Request().Input("chainId")
    resources, err := container.Get().ChainResourceRepo.FindByChainID(chainID)
    if err != nil {
        return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to load resources"})
    }
    return ctx.Response().Success().Json(http.Json{"data": resources})
}
```

- [ ] **Step 3: Compile and commit**

```bash
git add -A && git commit -m "feat: environment-filtered chain, token, resource endpoints"
```

---

### Task 14: Chain-environment validation on wallet creation

**Files:**
- Modify: `back/app/http/controllers/wallets_controller.go`

- [ ] **Step 1: Add environment validation to CreateWalletAdmin**

At the beginning of the `CreateWalletAdmin` handler, after request binding, add:

```go
// Validate chain matches account environment
env, _ := ctx.Value("account_environment").(string)
chainRecord, err := container.Get().ChainRepo.FindByID(req.Chain)
if err != nil || chainRecord == nil {
    return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "unsupported chain: " + req.Chain})
}
isTestnet := env == models.EnvironmentTest
if chainRecord.IsTestnet != isTestnet {
    return ctx.Response().Json(http.StatusForbidden, http.Json{
        "error": "chain " + req.Chain + " is not available in " + env + " environment",
    })
}
```

Also ensure the wallet is created with the account ID from context:

```go
accountID, _ := ctx.Value("account_id").(uuid.UUID)
// Use accountID when creating the wallet
```

- [ ] **Step 2: Compile and commit**

```bash
git add -A && git commit -m "feat: validate chain environment on wallet creation"
```

---

## Phase 5: Seeder

### Task 15: Full seeder rewrite

**Files:**
- Modify: `back/database/seeds/seeder.go`

- [ ] **Step 1: Rewrite seeder with paired accounts, chains, tokens, resources**

The seeder needs to:
1. Seed 8 chains (4 mainnet + 4 testnet) with encrypted RPC URLs
2. Seed tokens for each chain
3. Seed chain resources (explorers + faucets)
4. Create paired Acme Corp accounts (prod + test)
5. Create all 3 users with memberships on BOTH accounts
6. Set `default_account_id` for each user
7. Create wallets: mainnet wallets on prod account, testnet wallets on test account

The RPC URLs should be read from environment variables and encrypted via `facades.Crypt().EncryptString()` before inserting.

Key UUIDs to add:
```go
acmeTestAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000011")
tethWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000023")
tbtcWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000024")
tpolyWalletID     = uuid.MustParse("00000000-0000-0000-0000-000000000025")
```

Seed chains using `facades.Crypt().EncryptString(os.Getenv("ETH_RPC_URL"))` for each RPC URL. Use `TETH_RPC_URL`, `TBTC_RPC_URL`, `TPOLYGON_RPC_URL`, `TSOL_RPC_URL` for testnet chains.

**Token seed data — top tokens per chain:**

Ethereum (`eth`) mainnet tokens:
| Symbol | Name | Contract | Decimals |
|--------|------|----------|----------|
| USDT | Tether USD | 0xdAC17F958D2ee523a2206206994597C13D831ec7 | 6 |
| USDC | USD Coin | 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 | 6 |
| WETH | Wrapped Ether | 0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2 | 18 |
| WBTC | Wrapped Bitcoin | 0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599 | 8 |
| DAI | Dai Stablecoin | 0x6B175474E89094C44Da98b954EedeAC495271d0F | 18 |
| LINK | Chainlink | 0x514910771AF9Ca656af840dff83E8264EcF986CA | 18 |
| UNI | Uniswap | 0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984 | 18 |

Sepolia (`teth`) testnet tokens:
| Symbol | Name | Contract | Decimals |
|--------|------|----------|----------|
| USDT | Tether USD (Test) | 0x7169D38820dfd117C3FA1f22a697dBA58d90BA06 | 6 |
| USDC | USD Coin (Test) | 0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238 | 6 |
| LINK | Chainlink (Test) | 0x779877A7B0D9E8603169DdbD7836e478b4624789 | 18 |

Polygon (`polygon`) mainnet tokens:
| Symbol | Name | Contract | Decimals |
|--------|------|----------|----------|
| USDT | Tether USD | 0xc2132D05D31c914a87C6611C10748AEb04B58e8F | 6 |
| USDC | USD Coin | 0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359 | 6 |
| WETH | Wrapped Ether | 0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619 | 18 |
| WBTC | Wrapped Bitcoin | 0x1BFD67037B42Cf73acF2047067bd4F2C47D9BfD6 | 8 |
| DAI | Dai Stablecoin | 0x8f3Cf7ad23Cd3CaDbD9735AFf958023239c6A063 | 18 |
| LINK | Chainlink | 0x53E0bca35eC356BD5ddDFebbD1Fc0fD03FaBad39 | 18 |

Polygon Amoy (`tpolygon`) testnet tokens:
| Symbol | Name | Contract | Decimals |
|--------|------|----------|----------|
| USDT | Tether USD (Test) | 0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13 | 6 |
| USDC | USD Coin (Test) | 0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582 | 6 |

Solana (`sol`) mainnet tokens:
| Symbol | Name | Contract (Mint) | Decimals |
|--------|------|-----------------|----------|
| USDC | USD Coin | EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v | 6 |
| USDT | Tether USD | Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB | 6 |
| SOL (WSOL) | Wrapped SOL | So11111111111111111111111111111111111111112 | 9 |
| BONK | Bonk | DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263 | 5 |
| JUP | Jupiter | JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN | 6 |
| WIF | dogwifhat | EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm | 6 |

Solana Devnet (`tsol`) testnet tokens:
| Symbol | Name | Contract (Mint) | Decimals |
|--------|------|-----------------|----------|
| USDC | USD Coin (Test) | 4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU | 6 |

Bitcoin (`btc`, `tbtc`) — no tokens (native only).

- [ ] **Step 2: Run seeder and verify**

Run: `cd back && make db-reset && make db-seed`
Expected: All chains, tokens, resources, paired accounts, and wallets created.

- [ ] **Step 3: Verify server boots with DB-driven chains**

Run: `cd back && go run . artisan serve` (or the equivalent start command)
Expected: Log shows `container booted` with all 8 chain IDs.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: rewrite seeder with testnet chains, paired accounts, resources"
```

---

## Phase 6: Frontend

### Task 16: Update Redux auth slice and types

**Files:**
- Modify: `front/src/types/account.ts`
- Modify: `front/src/lib/store/auth.slice.ts`

- [ ] **Step 1: Update Account type**

In `front/src/types/account.ts`, add to the `Account` interface:

```typescript
export interface Account {
  id: string;
  name: string;
  status: "active" | "archived" | "frozen";
  view_all_wallets: boolean;
  environment: "prod" | "test";
  linked_account_id?: string;
  created_at: string;
}
```

- [ ] **Step 2: Update auth slice**

In `front/src/lib/store/auth.slice.ts`:

```typescript
export interface AuthState {
  user: User | null;
  token: string | null;
  refreshToken: string | null;
  accountId: string | null;
  account: Account | null;
  accounts: Account[] | null;
  isAuthenticated: boolean;
}

const initialState: AuthState = {
  user: null,
  token: null,
  refreshToken: null,
  accountId: null,
  account: null,
  accounts: null,
  isAuthenticated: false,
};
```

Add `Account` import and update the `login` reducer:

```typescript
login(
  state,
  action: PayloadAction<{
    user: User;
    token: string;
    refreshToken?: string;
    accountId: string;
    account: Account;
    accounts: Account[];
  }>
) {
  state.user = action.payload.user;
  state.token = action.payload.token;
  state.refreshToken = action.payload.refreshToken ?? null;
  state.accountId = action.payload.accountId;
  state.account = action.payload.account;
  state.accounts = action.payload.accounts;
  state.isAuthenticated = true;
},
setAccount(state, action: PayloadAction<{ accountId: string; account: Account }>) {
  state.accountId = action.payload.accountId;
  state.account = action.payload.account;
},
```

Export the new action:

```typescript
export const { login, logout, setToken, setAccountId, setUser, setAccount } = authSlice.actions;
```

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat: add environment to Account type and auth slice"
```

---

### Task 17: Add default-account API call and enhance switchAccount

**Files:**
- Modify: `front/src/lib/api/client.ts`
- Modify: `front/src/hooks/useAuth.ts`

- [ ] **Step 1: Add API call for default account**

In `front/src/lib/api/client.ts`, add to the `api.user` namespace:

```typescript
updateDefaultAccount: (accountId: string) =>
  authedRequest<{ account: Account }>("PATCH", "/v1/users/me/default-account", { account_id: accountId }),
```

Add `Account` to the imports from `@/types/account`.

- [ ] **Step 2: Enhance switchAccount in useAuth**

In `front/src/hooks/useAuth.ts`, update `switchAccount` to call the backend:

```typescript
const switchAccount = useCallback(
  async (newAccountId: string) => {
    const result = await api.user.updateDefaultAccount(newAccountId);
    dispatch(setAccount({ accountId: newAccountId, account: result.account }));
  },
  [dispatch]
);
```

Update imports to include `setAccount` from `auth.slice` and `api` from the client.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat: switchAccount calls backend to persist default account"
```

---

### Task 18: Environment toggle component

**Files:**
- Create: `front/src/components/Header/EnvironmentToggle.tsx`
- Modify: `front/src/components/Header/Header.tsx`

- [ ] **Step 1: Create EnvironmentToggle component**

Create `front/src/components/Header/EnvironmentToggle.tsx`:

```tsx
"use client";

import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/utils/cn";

export function EnvironmentToggle() {
  const { account, switchAccount } = useAuth();

  if (!account?.linked_account_id) return null;

  const isTestnet = account.environment === "test";

  const handleToggle = () => {
    if (account.linked_account_id) {
      void switchAccount(account.linked_account_id);
    }
  };

  return (
    <div className="hidden sm:flex items-center gap-2 ml-4">
      <div
        className={cn(
          "flex items-center rounded-md overflow-hidden text-[11px] font-semibold uppercase tracking-wide",
          "border",
          isTestnet ? "border-amber-500/40" : "border-divider dark:border-divider-dark"
        )}
      >
        <button
          type="button"
          onClick={isTestnet ? handleToggle : undefined}
          className={cn(
            "px-2.5 py-1 transition-colors",
            !isTestnet
              ? "bg-primary text-white"
              : "bg-surface dark:bg-surface-dark text-p-hint dark:text-p-hint-dark hover:text-p-primary"
          )}
        >
          Mainnet
        </button>
        <button
          type="button"
          onClick={!isTestnet ? handleToggle : undefined}
          className={cn(
            "px-2.5 py-1 transition-colors",
            isTestnet
              ? "bg-amber-500 text-black"
              : "bg-surface dark:bg-surface-dark text-p-hint dark:text-p-hint-dark hover:text-p-primary"
          )}
        >
          Testnet
        </button>
      </div>

      {isTestnet && (
        <span className="flex items-center gap-1.5 px-2.5 py-1 rounded text-[11px] font-bold uppercase tracking-wider bg-amber-500/10 border border-amber-500/30 text-amber-500">
          <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
          Test Mode
        </span>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Add toggle to Header and testnet visual signals**

In `front/src/components/Header/Header.tsx`:

Import the toggle:
```tsx
import { EnvironmentToggle } from "@/components/Header/EnvironmentToggle";
```

Add after the logo `<Link>` and before `{isAuthenticated && <WalletSelector />}`:
```tsx
{isAuthenticated && <EnvironmentToggle />}
```

Add testnet top border by wrapping the `<header>` with a conditional class. Replace the header opening tag logic to include testnet amber border:

```tsx
const { account } = useAuth();
const isTestnet = isAuthenticated && account?.environment === "test";
```

Add to the `<header>` className:
```tsx
className={cn(
  headerWrapper,
  "shrink-0 border-b border-divider dark:border-divider-dark bg-surface dark:bg-surface-dark",
  isTestnet && "border-t-2 border-t-amber-500 bg-gradient-to-r from-amber-500/5 to-transparent"
)}
```

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat: add environment toggle to topbar with testnet visual signals"
```

---

### Task 19: Update LoginForm to handle account data

**Files:**
- Modify: `front/src/components/Auth/LoginForm.tsx`

- [ ] **Step 1: Update login dispatch to include account data**

In the login success handler, update the dispatch to pass account data from the API response:

```typescript
dispatch(
  login({
    user: data.user,
    token: data.access_token,
    refreshToken: data.refresh_token,
    accountId: data.account_id || data.account?.id || "",
    account: data.account,
    accounts: data.accounts || [],
  })
);
```

- [ ] **Step 2: Commit**

```bash
git add -A && git commit -m "feat: login stores account environment data in Redux"
```

---

### Task 20: Update registration form with organization name

**Files:**
- Modify: `front/src/pages/login/register.tsx`

- [ ] **Step 1: Add organization_name field**

Add a new form field for "Organization Name" (required) to the registration form, between the "Full Name" and "Email" fields. Update the submit handler to include `organization_name` in the request body.

- [ ] **Step 2: Commit**

```bash
git add -A && git commit -m "feat: registration form includes organization name"
```

---

### Task 21: SWR cache keys include accountId

**Files:**
- Modify: `front/src/hooks/useWallets.ts` (and other SWR hooks)

- [ ] **Step 1: Update SWR keys to include accountId**

In all SWR hooks that call authenticated endpoints, prefix the SWR key with `accountId`. For example, in `useWallets.ts`:

```typescript
const { accountId } = useAuth();
const key = accountId ? `/v1/wallets?account=${accountId}` : null;
```

The `accountId` is only used for cache keying — the actual header is sent by `authedRequest`. When `accountId` changes (via toggle), SWR sees a new key and refetches.

Repeat for `useTransactions`, `useWallet`, and any other SWR hooks.

- [ ] **Step 2: Commit**

```bash
git add -A && git commit -m "feat: SWR cache keys include accountId for environment isolation"
```

---

## Phase 7: End-to-End Verification

### Task 22: Full integration test

- [ ] **Step 1: Reset and reseed database**

```bash
cd back && make db-reset && make db-seed
```

- [ ] **Step 2: Start the backend**

```bash
cd back && make serve
```
Expected: Logs show all 8 chains loaded from DB.

- [ ] **Step 3: Start the frontend**

```bash
cd front && npm run dev
```

- [ ] **Step 4: Login as admin@vault.dev**

Open `http://localhost:2001/login`, login with `admin@vault.dev / Password123!`
Expected: Dashboard loads with "Acme Corp" account, environment toggle shows "Mainnet" active (green).

- [ ] **Step 5: Verify mainnet wallet creation**

Click "Create Wallet", select `eth` chain.
Expected: Wallet created successfully, listed on assets page as an ETH wallet.

- [ ] **Step 6: Switch to testnet**

Click the "Testnet" toggle in the topbar.
Expected: Topbar changes to amber, badge shows "Test Mode", wallets list changes to show testnet wallets.

- [ ] **Step 7: Verify testnet wallet creation**

Click "Create Wallet", select `teth` chain.
Expected: Wallet created on Sepolia testnet, listed with `teth` chain label.

- [ ] **Step 8: Verify environment isolation**

While on testnet, confirm that only testnet chains appear in chain listings. Switch back to mainnet and confirm only mainnet chains appear. Wallets from one environment should not appear in the other.

- [ ] **Step 9: Commit any fixes needed**

```bash
git add -A && git commit -m "fix: integration fixes for testnet environment support"
```

- [ ] **Step 10: Final commit**

```bash
git add -A && git commit -m "feat: testnet environment support complete"
```
