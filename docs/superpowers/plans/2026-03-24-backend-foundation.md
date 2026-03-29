# Backend Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multitenant account structure, user authentication (JWT via Goravel facades), 2FA, password recovery, account management, and access token management to the Vault Go backend.

**Architecture:** All new code follows the existing Goravel pattern — controllers are free functions receiving `http.Context`, middleware functions have signature `func(ctx http.Context)`, routes use `facades.Route()`, models use GORM via Goravel ORM, config is added to `config/app.go` via `facades.Config().Add()`. Tests use `suite.Suite` + `goravelTesting.TestCase` with real DB (see existing `helpers_test.go`).

**Tech Stack:** Go 1.22, Goravel v1.17.2 + Gin adapter, PostgreSQL, `facades.Auth()` (JWT), `facades.Gate()` (RBAC), `facades.Mail()` (queued email), `facades.Crypt()` (AES-256-GCM), `golang.org/x/crypto/bcrypt` (token hashing), `github.com/pquerna/otp` (TOTP).

**Spec:** `docs/superpowers/specs/2026-03-24-admin-panel-design.md`

---

## File Map

**New migrations** (`back/database/migrations/`):
- `20260324000001_create_users_table.go`
- `20260324000002_create_accounts_table.go`
- `20260324000003_create_account_users_table.go`
- `20260324000004_create_access_tokens_table.go`
- `20260324000005_create_refresh_tokens_table.go`
- `20260324000006_create_password_reset_tokens_table.go`
- `20260324000007_create_totp_recovery_codes_table.go`
- `20260324000008_alter_wallets_add_account_fields.go`
- `20260324000009_alter_addresses_add_metadata.go`
- `20260324000010_alter_webhooks_add_wallet_id.go`
- `20260324000011_alter_transactions_add_wallet_id.go`
- `20260324000012_create_withdrawals_table.go`
- `20260324000013_create_wallet_users_table.go`
- `20260324000014_create_whitelist_entries_table.go`

**New models** (`back/app/models/`):
- `user.go`
- `account.go`
- `account_user.go`
- `access_token.go`
- `refresh_token.go`
- `password_reset_token.go`
- `totp_recovery_code.go`
- `wallet_user.go`
- `whitelist_entry.go`
- `withdrawal.go`

**Modified models:**
- `wallet.go` — add `AccountID`, `Status`, `FeeRateMin`, `FeeRateMax`, `FeeMultiplier`, `RequiredApprovals`, `FrozenUntil`
- `address.go` — add `Label`, `CreatedBy`
- `webhook_config.go` — add `WalletID`
- `transaction.go` — add `WalletID` FK

**Config:**
- `config/app.go` — add JWT, auth, mail, crypt config blocks + service providers

**New services** (`back/app/services/`):
- `auth/service.go` + `auth/service_test.go`
- `account/service.go` + `account/service_test.go`

**New mailables** (`back/app/mail/`):
- `password_reset_mail.go`
- `welcome_mail.go`
- `user_invite_mail.go`

**New policies** (`back/app/policies/`):
- `account_policy.go`
- `wallet_policy.go`

**New providers** (`back/app/providers/`):
- `auth_service_provider.go` — registers gates + policies

**New middleware** (`back/app/http/middleware/`):
- `session_auth.go`
- `account_context.go`
- `chain_type.go`

**New controllers** (`back/app/http/controllers/`):
- `auth_controller.go` + `auth_controller_test.go`
- `account_controller.go` + `account_controller_test.go`
- `user_controller.go` + `user_controller_test.go`

**New requests** (`back/app/http/requests/`):
- `login_request.go`, `register_request.go`, `verify_2fa_request.go`
- `recover_request.go`, `recover_confirm_request.go`
- `create_account_request.go`, `update_account_request.go`
- `add_account_user_request.go`, `create_access_token_request.go`
- `update_profile_request.go`, `change_password_request.go`

**Modified:**
- `routes/api.go` — add all new auth + account + user routes
- `database/migrations/migrations.go` — register new migrations
- `app/container/container.go` — add AuthService, AccountService

---

## Task 1: Database Migrations

**Files:**
- Create: `back/database/migrations/20260324000001_create_users_table.go` through `20260324000014_create_whitelist_entries_table.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1.1: Create users migration**

```go
// back/database/migrations/20260324000001_create_users_table.go
package migrations

import (
    "github.com/goravel/framework/contracts/database/schema"
    "github.com/goravel/framework/facades"
)

type M20260324000001CreateUsersTable struct{}

func (r *M20260324000001CreateUsersTable) Signature() string {
    return "20260324000001_create_users_table"
}

func (r *M20260324000001CreateUsersTable) Up() error {
    return facades.Schema().Create("users", func(table schema.Blueprint) {
        table.Uuid("id")
        table.Primary("id")
        table.String("email", 255).Unique()
        table.Text("password_hash")
        table.String("full_name", 255).Nullable()
        table.Text("totp_secret").Nullable()
        table.Boolean("totp_enabled").Default(false)
        table.String("status", 20).Default("active")
        table.Timestamps()
        table.Index("email")
    })
}

func (r *M20260324000001CreateUsersTable) Down() error {
    return facades.Schema().DropIfExists("users")
}
```

- [ ] **Step 1.2: Create accounts migration**

```go
// back/database/migrations/20260324000002_create_accounts_table.go
package migrations

import (
    "github.com/goravel/framework/contracts/database/schema"
    "github.com/goravel/framework/facades"
)

type M20260324000002CreateAccountsTable struct{}

func (r *M20260324000002CreateAccountsTable) Signature() string {
    return "20260324000002_create_accounts_table"
}

func (r *M20260324000002CreateAccountsTable) Up() error {
    return facades.Schema().Create("accounts", func(table schema.Blueprint) {
        table.Uuid("id")
        table.Primary("id")
        table.String("name", 255)
        table.String("status", 20).Default("active")
        table.Boolean("view_all_wallets").Default(false)
        table.Timestamps()
    })
}

func (r *M20260324000002CreateAccountsTable) Down() error {
    return facades.Schema().DropIfExists("accounts")
}
```

- [ ] **Step 1.3: Create account_users migration**

```go
// back/database/migrations/20260324000003_create_account_users_table.go
package migrations

import (
    "github.com/goravel/framework/contracts/database/schema"
    "github.com/goravel/framework/facades"
)

type M20260324000003CreateAccountUsersTable struct{}

func (r *M20260324000003CreateAccountUsersTable) Signature() string {
    return "20260324000003_create_account_users_table"
}

func (r *M20260324000003CreateAccountUsersTable) Up() error {
    err := facades.Schema().Create("account_users", func(table schema.Blueprint) {
        table.Uuid("id")
        table.Primary("id")
        table.Uuid("account_id")
        table.Uuid("user_id")
        table.String("role", 20)       // owner | admin | auditor | user
        table.String("status", 20).Default("active")
        table.Uuid("added_by").Nullable()
        table.Timestamp("deleted_at").Nullable()
        table.Timestamps()
        table.Foreign("account_id").References("id").On("accounts").OnDelete("CASCADE")
        table.Foreign("user_id").References("id").On("users").OnDelete("CASCADE")
        table.Index("account_id")
        table.Index("user_id")
    })
    if err != nil {
        return err
    }
    // Partial unique index: one active membership per (account, user)
    return facades.DB().Statement.DB.Exec(
        `CREATE UNIQUE INDEX account_users_active_unique ON account_users (account_id, user_id) WHERE deleted_at IS NULL`,
    ).Error
}

func (r *M20260324000003CreateAccountUsersTable) Down() error {
    return facades.Schema().DropIfExists("account_users")
}
```

- [ ] **Step 1.4: Create remaining migrations** — follow the same pattern for:

  `20260324000004_create_access_tokens_table.go`:
  ```
  columns: id(uuid/pk), account_id(uuid/fk→accounts), created_by(uuid/fk→users nullable),
  name(varchar255), token_hash(text), permissions(text[]), ip_cidr(text nullable),
  spending_limit(jsonb nullable), valid_until(timestamp nullable), timestamps
  index: account_id
  ```

  `20260324000005_create_refresh_tokens_table.go`:
  ```
  columns: id(uuid/pk), user_id(uuid/fk→users cascade), token_hash(text),
  expires_at(timestamp), revoked_at(timestamp nullable), timestamps
  index: user_id
  ```

  `20260324000006_create_password_reset_tokens_table.go`:
  ```
  columns: id(uuid/pk), user_id(uuid/fk→users cascade), token_hash(text),
  expires_at(timestamp), used_at(timestamp nullable), timestamps
  ```

  `20260324000007_create_totp_recovery_codes_table.go`:
  ```
  columns: id(uuid/pk), user_id(uuid/fk→users cascade), code_hash(text),
  used_at(timestamp nullable), timestamps
  index: user_id
  ```

  `20260324000008_alter_wallets_add_account_fields.go`:
  ```go
  func (r *...) Up() error {
      return facades.Schema().Table("wallets", func(table schema.Blueprint) {
          table.Uuid("account_id").Nullable()
          table.String("status", 20).Default("active")
          table.Integer("fee_rate_min").Nullable()
          table.Integer("fee_rate_max").Nullable()
          table.Decimal("fee_multiplier", 8, 4).Nullable()
          table.Integer("required_approvals").Default(1)
          table.Timestamp("frozen_until").Nullable()
      })
  }
  ```

  `20260324000009_alter_addresses_add_metadata.go`:
  ```
  add: label(varchar255 nullable), created_by(uuid nullable fk→users)
  ```

  `20260324000010_alter_webhooks_add_wallet_id.go`:
  ```
  add: wallet_id(uuid nullable fk→wallets)
  ```

  `20260324000011_alter_transactions_add_wallet_id.go`:
  ```
  add: wallet_id(uuid nullable fk→wallets)
  ```

  `20260324000012_create_withdrawals_table.go`:
  ```
  columns: id(uuid/pk), wallet_id(uuid/fk→wallets), transaction_id(uuid nullable fk→transactions),
  account_id(uuid/fk→accounts), status(varchar20 default pending), amount(decimal),
  destination_address(text), fee_estimate(decimal nullable), note(text nullable),
  created_by(uuid nullable fk→users), timestamps
  index: wallet_id, account_id
  ```

  `20260324000013_create_wallet_users_table.go` — same as account_users but for wallets, roles as `text[]`, partial unique index `wallet_users_active_unique ON wallet_users (wallet_id, user_id) WHERE deleted_at IS NULL`

  `20260324000014_create_whitelist_entries_table.go`:
  ```
  columns: id(uuid/pk), wallet_id(uuid/fk→wallets cascade), label(varchar255 nullable), address(text), timestamps
  index: wallet_id
  ```

- [ ] **Step 1.5: Register all new migrations in `migrations.go`**

```go
// back/database/migrations/migrations.go — add to the returned slice:
&M20260324000001CreateUsersTable{},
&M20260324000002CreateAccountsTable{},
// ... all 14 new migrations in order
```

- [ ] **Step 1.6: Run migrations**
```bash
cd back && go run . artisan migrate
```
Expected: all 14 migrations run successfully, no errors.

- [ ] **Step 1.7: Commit**
```bash
git add back/database/migrations/
git commit -m "feat: add multitenant schema migrations"
```

---

## Task 2: Models

**Files:** Create `back/app/models/user.go`, `account.go`, `account_user.go`, `access_token.go`, `refresh_token.go`, `password_reset_token.go`, `totp_recovery_code.go`, `wallet_user.go`, `whitelist_entry.go`, `withdrawal.go`

- [ ] **Step 2.1: Create User model**

```go
// back/app/models/user.go
package models

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/database/orm"
)

type User struct {
    orm.Model
    ID           uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    Email        string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
    PasswordHash string    `gorm:"type:text;not null" json:"-"`
    FullName     string    `gorm:"type:varchar(255)" json:"full_name,omitempty"`
    TotpSecret   string    `gorm:"type:text" json:"-"` // encrypted with facades.Crypt()
    TotpEnabled  bool      `gorm:"default:false" json:"totp_enabled"`
    Status       string    `gorm:"type:varchar(20);default:active" json:"status"`
}

func (u *User) TableName() string { return "users" }
```

- [ ] **Step 2.2: Create Account model**

```go
// back/app/models/account.go
package models

import (
    "github.com/google/uuid"
    "github.com/goravel/framework/database/orm"
)

type Account struct {
    orm.Model
    ID              uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    Name            string    `gorm:"type:varchar(255);not null" json:"name"`
    Status          string    `gorm:"type:varchar(20);default:active" json:"status"`
    ViewAllWallets  bool      `gorm:"default:false" json:"view_all_wallets"`
}

func (a *Account) TableName() string { return "accounts" }
```

- [ ] **Step 2.3: Create AccountUser model**

```go
// back/app/models/account_user.go
package models

import (
    "time"
    "github.com/google/uuid"
    "github.com/goravel/framework/database/orm"
)

type AccountUser struct {
    orm.Model
    ID        uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
    AccountID uuid.UUID  `gorm:"type:uuid;not null;index" json:"account_id"`
    UserID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
    Role      string     `gorm:"type:varchar(20);not null" json:"role"` // owner|admin|auditor|user
    Status    string     `gorm:"type:varchar(20);default:active" json:"status"`
    AddedBy   *uuid.UUID `gorm:"type:uuid" json:"added_by,omitempty"`
    DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
    User      *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (a *AccountUser) TableName() string { return "account_users" }
```

- [ ] **Step 2.4: Create remaining models** — follow same pattern:

  `access_token.go`: ID, AccountID, CreatedBy(*uuid), Name, TokenHash(json:"-"), Permissions(pq.StringArray), IpCidr, SpendingLimit(datatypes.JSON), ValidUntil(*time.Time)

  `refresh_token.go`: ID, UserID, TokenHash(json:"-"), ExpiresAt, RevokedAt(*time.Time)

  `password_reset_token.go`: ID, UserID, TokenHash(json:"-"), ExpiresAt, UsedAt(*time.Time)

  `totp_recovery_code.go`: ID, UserID, CodeHash(json:"-"), UsedAt(*time.Time)

  `wallet_user.go`: ID, WalletID, UserID, Roles(pq.StringArray), Status, DeletedAt(*time.Time), User(*User)

  `whitelist_entry.go`: ID, WalletID, Label, Address, timestamps

  `withdrawal.go`: ID, WalletID, TransactionID(*uuid), AccountID, Status, Amount(decimal.Decimal), DestinationAddress, FeeEstimate(*decimal), Note, CreatedBy(*uuid)

- [ ] **Step 2.5: Update existing models**

  `wallet.go` — add fields:
  ```go
  AccountID         *uuid.UUID `gorm:"type:uuid;index" json:"account_id,omitempty"`
  Status            string     `gorm:"type:varchar(20);default:active" json:"status"`
  FeeRateMin        *int       `gorm:"type:integer" json:"fee_rate_min,omitempty"`
  FeeRateMax        *int       `gorm:"type:integer" json:"fee_rate_max,omitempty"`
  FeeMultiplier     *float64   `gorm:"type:decimal(8,4)" json:"fee_multiplier,omitempty"`
  RequiredApprovals int        `gorm:"default:1" json:"required_approvals"`
  FrozenUntil       *time.Time `json:"frozen_until,omitempty"`
  ```

  `address.go` — add `Label string` and `CreatedBy *uuid.UUID`

  `webhook_config.go` — add `WalletID *uuid.UUID gorm:"type:uuid;index"`

  `transaction.go` — add `WalletID *uuid.UUID gorm:"type:uuid;index"`

- [ ] **Step 2.6: Commit**
```bash
git add back/app/models/
git commit -m "feat: add user, account, and multitenant models"
```

---

## Task 3: Config Updates

**Files:** Modify `back/config/app.go`

- [ ] **Step 3.1: Add required dependencies**
```bash
cd back && go get github.com/pquerna/otp golang.org/x/crypto
```

- [ ] **Step 3.2: Add auth, JWT, mail, and crypt config to `Boot()` in `config/app.go`**

Add after the existing `facades.Config().Add("http", ...)` call:

```go
// Auth guards
facades.Config().Add("auth", map[string]any{
    "defaults": map[string]any{"guard": "web"},
    "guards": map[string]any{
        "web": map[string]any{
            "driver":   "jwt",
            "provider": "users",
        },
    },
    "providers": map[string]any{
        "users": map[string]any{
            "driver": "orm",
            "model":  models.User{},
        },
    },
})

// JWT settings
facades.Config().Add("jwt", map[string]any{
    "secret":      env("JWT_SECRET", ""),
    "ttl":         envInt("JWT_TTL", 15),        // minutes
    "refresh_ttl": envInt("JWT_REFRESH_TTL", 43200), // 30 days in minutes
})

// Mail
facades.Config().Add("mail", map[string]any{
    "default": "smtp",
    "mailers": map[string]any{
        "smtp": map[string]any{
            "transport":  "smtp",
            "host":       env("MAIL_HOST", ""),
            "port":       envInt("MAIL_PORT", 587),
            "encryption": env("MAIL_ENCRYPTION", "tls"),
            "username":   env("MAIL_USERNAME", ""),
            "password":   env("MAIL_PASSWORD", ""),
        },
    },
    "from": map[string]any{
        "address": env("MAIL_FROM_ADDRESS", "noreply@vault.dev"),
        "name":    env("MAIL_FROM_NAME", "Vault"),
    },
})
```

- [ ] **Step 3.3: Add JWT + Mail service providers to the `providers` array in `config/app.go`**

```go
// In the "providers" slice inside facades.Config().Add("app", ...):
&frameworkauth.ServiceProvider{},  // import "github.com/goravel/framework/auth"
&frameworkmail.ServiceProvider{},  // import "github.com/goravel/framework/mail"
```

- [ ] **Step 3.4: Generate JWT secret**
```bash
cd back && go run . artisan jwt:secret
```
Expected: `JWT_SECRET=<generated>` written to `.env`.

- [ ] **Step 3.5: Build to verify no compile errors**
```bash
cd back && go build ./...
```
Expected: no errors.

- [ ] **Step 3.6: Commit**
```bash
git add back/config/ back/go.mod back/go.sum
git commit -m "feat: add auth, JWT, and mail config"
```

---

## Task 4: Auth Service

**Files:**
- Create: `back/app/services/auth/service.go`
- Create: `back/app/services/auth/service_test.go`

- [ ] **Step 4.1: Write the failing tests**

```go
// back/app/services/auth/service_test.go
package auth_test

import (
    "testing"
    "github.com/stretchr/testify/suite"
    "github.com/stretchr/testify/assert"
    "github.com/macromarkets/vault/app/services/auth"
)

type AuthServiceTestSuite struct {
    suite.Suite
}

func TestAuthService(t *testing.T) {
    suite.Run(t, new(AuthServiceTestSuite))
}

func (s *AuthServiceTestSuite) TestHashPassword_ReturnsBcryptHash() {
    svc := auth.NewService()
    hash, err := svc.HashPassword("mysecret")
    s.NoError(err)
    s.NotEmpty(hash)
    s.True(svc.CheckPassword("mysecret", hash))
}

func (s *AuthServiceTestSuite) TestCheckPassword_WrongPassword_ReturnsFalse() {
    svc := auth.NewService()
    hash, _ := svc.HashPassword("correct")
    s.False(svc.CheckPassword("wrong", hash))
}

func (s *AuthServiceTestSuite) TestGenerateTOTP_ReturnsKeyAndQR() {
    svc := auth.NewService()
    key, qr, err := svc.GenerateTOTP("user@example.com")
    s.NoError(err)
    s.NotEmpty(key)
    s.NotEmpty(qr)
}

func (s *AuthServiceTestSuite) TestVerifyTOTP_ValidCode_ReturnsTrue() {
    svc := auth.NewService()
    key, _, _ := svc.GenerateTOTP("user@example.com")
    code, err := totp.GenerateCode(key, time.Now())
    s.NoError(err)
    s.True(svc.VerifyTOTP(key, code))
}

func (s *AuthServiceTestSuite) TestGenerateRecoveryCodes_Returns10Codes() {
    svc := auth.NewService()
    codes, hashes, err := svc.GenerateRecoveryCodes()
    s.NoError(err)
    s.Len(codes, 10)
    s.Len(hashes, 10)
}

func (s *AuthServiceTestSuite) TestVerifyRecoveryCode_MatchesHash() {
    svc := auth.NewService()
    codes, hashes, _ := svc.GenerateRecoveryCodes()
    s.True(svc.VerifyRecoveryCode(codes[0], hashes[0]))
    s.False(svc.VerifyRecoveryCode(codes[0], hashes[1]))
}

func (s *AuthServiceTestSuite) TestHashToken_IsDeterministicInCheck() {
    svc := auth.NewService()
    raw := "some-refresh-token"
    hash := svc.HashToken(raw)
    s.True(svc.CheckToken(raw, hash))
    s.False(svc.CheckToken("other-token", hash))
}
```

- [ ] **Step 4.2: Run tests to verify they fail**
```bash
cd back && go test ./app/services/auth/... -v 2>&1 | head -20
```
Expected: compilation error (package doesn't exist yet).

- [ ] **Step 4.3: Implement auth service**

```go
// back/app/services/auth/service.go
package auth

import (
    "crypto/rand"
    "encoding/base32"
    "fmt"

    "github.com/pquerna/otp/totp"
    "golang.org/x/crypto/bcrypt"
)

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) HashPassword(password string) (string, error) {
    bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    return string(bytes), err
}

func (s *Service) CheckPassword(password, hash string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) GenerateTOTP(email string) (secret, qrURL string, err error) {
    key, err := totp.Generate(totp.GenerateOpts{
        Issuer:      "Vault",
        AccountName: email,
    })
    if err != nil {
        return "", "", err
    }
    return key.Secret(), key.URL(), nil
}

func (s *Service) VerifyTOTP(secret, code string) bool {
    return totp.Validate(code, secret)
}

// GenerateRecoveryCodes returns 10 plaintext codes and their bcrypt hashes.
func (s *Service) GenerateRecoveryCodes() (codes []string, hashes []string, err error) {
    for i := 0; i < 10; i++ {
        b := make([]byte, 10)
        if _, err = rand.Read(b); err != nil {
            return nil, nil, err
        }
        code := base32.StdEncoding.EncodeToString(b)[:16]
        hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
        if err != nil {
            return nil, nil, err
        }
        codes = append(codes, code)
        hashes = append(hashes, string(hash))
    }
    return codes, hashes, nil
}

func (s *Service) VerifyRecoveryCode(code, hash string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil
}

// HashToken produces a bcrypt hash of a raw token (for refresh/reset tokens).
func (s *Service) HashToken(raw string) string {
    hash, _ := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
    return string(hash)
}

func (s *Service) CheckToken(raw, hash string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil
}

// GenerateRandomToken returns a cryptographically secure URL-safe token.
func (s *Service) GenerateRandomToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", fmt.Errorf("generate token: %w", err)
    }
    return base32.StdEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 4.4: Run tests to verify they pass**
```bash
cd back && go test ./app/services/auth/... -v
```
Expected: all tests PASS.

- [ ] **Step 4.5: Commit**
```bash
git add back/app/services/auth/
git commit -m "feat: add auth service (bcrypt, TOTP, recovery codes)"
```

---

## Task 5: Account Service

**Files:**
- Create: `back/app/services/account/service.go`
- Create: `back/app/services/account/service_test.go`

- [ ] **Step 5.1: Write failing tests**

```go
// back/app/services/account/service_test.go
package account_test

import (
    "context"
    "testing"
    "github.com/google/uuid"
    "github.com/stretchr/testify/suite"
    goravelTesting "github.com/goravel/framework/testing"
    "github.com/macromarkets/vault/app/services/account"
    "github.com/macromarkets/vault/tests/mocks"
)

type AccountServiceTestSuite struct {
    suite.Suite
    goravelTesting.TestCase
    svc *account.Service
}

func TestAccountService(t *testing.T) { suite.Run(t, new(AccountServiceTestSuite)) }

func (s *AccountServiceTestSuite) SetupTest() {
    mocks.TestDB(s.T())
    s.svc = account.NewService()
}

func (s *AccountServiceTestSuite) TestCreate_Success() {
    ownerID := uuid.New()
    acc, err := s.svc.Create(context.Background(), "Test Account", ownerID)
    s.NoError(err)
    s.Equal("Test Account", acc.Name)
    s.Equal("active", acc.Status)
}

func (s *AccountServiceTestSuite) TestAddUser_Success() {
    ownerID := uuid.New()
    userID := uuid.New()
    acc, _ := s.svc.Create(context.Background(), "Test", ownerID)
    err := s.svc.AddUser(context.Background(), acc.ID, userID, "admin", ownerID)
    s.NoError(err)
}

func (s *AccountServiceTestSuite) TestAddUser_ReAdd_ClearsDeletedAt() {
    ownerID, userID := uuid.New(), uuid.New()
    acc, _ := s.svc.Create(context.Background(), "Test", ownerID)
    _ = s.svc.AddUser(context.Background(), acc.ID, userID, "admin", ownerID)
    _ = s.svc.RemoveUser(context.Background(), acc.ID, userID)
    err := s.svc.AddUser(context.Background(), acc.ID, userID, "auditor", ownerID)
    s.NoError(err)
}

func (s *AccountServiceTestSuite) TestIsolation_UserCannotAccessOtherAccount() {
    ownerA, ownerB := uuid.New(), uuid.New()
    accA, _ := s.svc.Create(context.Background(), "A", ownerA)
    accB, _ := s.svc.Create(context.Background(), "B", ownerB)
    role, err := s.svc.GetUserRole(context.Background(), accB.ID, ownerA)
    s.NoError(err)
    s.Empty(role) // ownerA has no role in accB
    _ = accA
}
```

- [ ] **Step 5.2: Run tests to verify they fail**
```bash
cd back && go test ./app/services/account/... -v 2>&1 | head -5
```
Expected: compilation error.

- [ ] **Step 5.3: Implement account service**

```go
// back/app/services/account/service.go
package account

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/goravel/framework/facades"

    "github.com/macromarkets/vault/app/models"
)

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Create(ctx context.Context, name string, ownerID uuid.UUID) (*models.Account, error) {
    acc := &models.Account{ID: uuid.New(), Name: name, Status: "active"}
    if err := facades.Orm().Query().Create(acc); err != nil {
        return nil, err
    }
    membership := &models.AccountUser{
        ID: uuid.New(), AccountID: acc.ID, UserID: ownerID, Role: "owner",
    }
    if err := facades.Orm().Query().Create(membership); err != nil {
        return nil, err
    }
    return acc, nil
}

func (s *Service) GetUserRole(ctx context.Context, accountID, userID uuid.UUID) (string, error) {
    var au models.AccountUser
    err := facades.Orm().Query().
        Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
        First(&au)
    if err != nil {
        return "", nil // not a member
    }
    return au.Role, nil
}

func (s *Service) AddUser(ctx context.Context, accountID, userID uuid.UUID, role string, addedBy uuid.UUID) error {
    // Re-add: clear deleted_at if row exists
    var existing models.AccountUser
    err := facades.Orm().Query().
        Where("account_id = ? AND user_id = ?", accountID, userID).
        First(&existing)
    if err == nil && existing.DeletedAt != nil {
        return facades.Orm().Query().
            Model(&existing).
            Update("deleted_at", nil).
            Update("role", role).Error
    }
    au := &models.AccountUser{
        ID: uuid.New(), AccountID: accountID, UserID: userID,
        Role: role, AddedBy: &addedBy,
    }
    return facades.Orm().Query().Create(au)
}

func (s *Service) RemoveUser(ctx context.Context, accountID, userID uuid.UUID) error {
    now := time.Now()
    return facades.Orm().Query().
        Model(&models.AccountUser{}).
        Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
        Update("deleted_at", now)
}
```

- [ ] **Step 5.4: Run tests to verify they pass**
```bash
cd back && go test ./app/services/account/... -v
```
Expected: all PASS.

- [ ] **Step 5.5: Commit**
```bash
git add back/app/services/account/
git commit -m "feat: add account service with multitenant isolation"
```

---

## Task 6: Mail Mailables

**Files:**
- Create: `back/app/mail/password_reset_mail.go`
- Create: `back/app/mail/welcome_mail.go`
- Create: `back/app/mail/user_invite_mail.go`
- Create: `back/resources/views/mail/password_reset.html`
- Create: `back/resources/views/mail/welcome.html`
- Create: `back/resources/views/mail/user_invite.html`

- [ ] **Step 6.1: Create password reset mailable**

```go
// back/app/mail/password_reset_mail.go
package mail

import (
    "github.com/goravel/framework/contracts/mail"
)

type PasswordResetMail struct {
    ToEmail   string
    ToName    string
    ResetLink string
}

func NewPasswordResetMail(email, name, resetLink string) *PasswordResetMail {
    return &PasswordResetMail{ToEmail: email, ToName: name, ResetLink: resetLink}
}

func (m *PasswordResetMail) Envelope() mail.Envelope {
    return mail.Envelope{
        To:      []mail.Address{{Address: m.ToEmail, Name: m.ToName}},
        Subject: "Reset your Vault password",
    }
}

func (m *PasswordResetMail) Content() mail.Content {
    return mail.Content{
        Html: &mail.Html{
            View: "mail/password_reset",
            Data: map[string]any{
                "Name":      m.ToName,
                "ResetLink": m.ResetLink,
            },
        },
    }
}

func (m *PasswordResetMail) Queue() *mail.Queue { return nil }
func (m *PasswordResetMail) Attachments() []mail.Attachment { return nil }
func (m *PasswordResetMail) Headers() map[string]string { return nil }
```

- [ ] **Step 6.2: Create welcome and invite mailables** — same structure, different subjects/templates.

- [ ] **Step 6.3: Create HTML templates** in `back/resources/views/mail/`:

```html
<!-- password_reset.html -->
<!DOCTYPE html>
<html>
<body>
  <h2>Password Reset</h2>
  <p>Hi {{.Name}},</p>
  <p>Click the link below to reset your Vault password. This link expires in 1 hour.</p>
  <p><a href="{{.ResetLink}}">Reset Password</a></p>
  <p>If you didn't request this, ignore this email.</p>
</body>
</html>
```

- [ ] **Step 6.4: Build to verify**
```bash
cd back && go build ./...
```

- [ ] **Step 6.5: Commit**
```bash
git add back/app/mail/ back/resources/
git commit -m "feat: add password reset, welcome, and invite mailables"
```

---

## Task 7: Policies + Auth Service Provider

**Files:**
- Create: `back/app/policies/account_policy.go`
- Create: `back/app/policies/wallet_policy.go`
- Create: `back/app/providers/auth_service_provider.go`

- [ ] **Step 7.1: Create account policy**

```go
// back/app/policies/account_policy.go
package policies

import (
    "context"
    "github.com/goravel/framework/contracts/access"
)

// AccountPolicy defines authorization rules for account resources.
// Arguments map always contains "account_id" (uuid.UUID) and "user_role" (string).
type AccountPolicy struct{}

func (p *AccountPolicy) Admin(ctx context.Context, args map[string]any) access.Response {
    role, _ := args["user_role"].(string)
    if role == "owner" || role == "admin" {
        return access.NewAllowResponse()
    }
    return access.NewDenyResponse("requires admin or owner role")
}

func (p *AccountPolicy) Owner(ctx context.Context, args map[string]any) access.Response {
    role, _ := args["user_role"].(string)
    if role == "owner" {
        return access.NewAllowResponse()
    }
    return access.NewDenyResponse("requires owner role")
}

func (p *AccountPolicy) View(ctx context.Context, args map[string]any) access.Response {
    role, _ := args["user_role"].(string)
    if role != "" {
        return access.NewAllowResponse()
    }
    return access.NewDenyResponse("not a member of this account")
}
```

- [ ] **Step 7.2: Create wallet policy**

```go
// back/app/policies/wallet_policy.go — same pattern
// Gates: wallet:spend, wallet:admin, wallet:view
// Arguments: "wallet_roles" ([]string)
```

- [ ] **Step 7.3: Create auth service provider**

```go
// back/app/providers/auth_service_provider.go
package providers

import (
    "github.com/goravel/framework/facades"
    "github.com/macromarkets/vault/app/policies"
)

type AuthServiceProvider struct{}

func (p *AuthServiceProvider) Register(app any) {}

func (p *AuthServiceProvider) Boot(app any) {
    policy := &policies.AccountPolicy{}
    facades.Gate().Define("account:view", policy.View)
    facades.Gate().Define("account:admin", policy.Admin)
    facades.Gate().Define("account:owner", policy.Owner)

    wp := &policies.WalletPolicy{}
    facades.Gate().Define("wallet:view", wp.View)
    facades.Gate().Define("wallet:spend", wp.Spend)
    facades.Gate().Define("wallet:admin", wp.Admin)
}
```

- [ ] **Step 7.4: Register provider in `config/app.go` providers slice**

```go
&providers.AuthServiceProvider{},
```

- [ ] **Step 7.5: Build to verify**
```bash
cd back && go build ./...
```

- [ ] **Step 7.6: Commit**
```bash
git add back/app/policies/ back/app/providers/
git commit -m "feat: add account and wallet authorization policies"
```

---

## Task 8: Middleware

**Files:**
- Create: `back/app/http/middleware/session_auth.go`
- Create: `back/app/http/middleware/account_context.go`
- Create: `back/app/http/middleware/chain_type.go`

- [ ] **Step 8.1: Write session auth middleware test**

```go
// back/app/http/middleware/session_auth_test.go
func TestSessionAuth_MissingToken_Returns401(t *testing.T) { ... }
func TestSessionAuth_InvalidToken_Returns401(t *testing.T) { ... }
func TestSessionAuth_ValidToken_SetsUserID(t *testing.T) { ... }
```

- [ ] **Step 8.2: Implement session auth middleware**

```go
// back/app/http/middleware/session_auth.go
package middleware

import (
    "strings"
    "github.com/goravel/framework/contracts/http"
    "github.com/goravel/framework/facades"
)

// SessionAuth validates Bearer JWT and populates user_id in context.
func SessionAuth(ctx http.Context) {
    token := ctx.Request().Header("Authorization", "")
    token = strings.TrimPrefix(token, "Bearer ")
    if token == "" {
        ctx.Request().AbortWithStatus(http.StatusUnauthorized)
        ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "missing token"})
        return
    }
    payload, err := facades.Auth(ctx).Guard("web").Parse(token)
    if err != nil || payload == nil {
        ctx.Request().AbortWithStatus(http.StatusUnauthorized)
        ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token"})
        return
    }
    ctx.WithValue("user_id", payload.Key)
    ctx.Request().Next()
}
```

- [ ] **Step 8.3: Implement account context middleware**

```go
// back/app/http/middleware/account_context.go
package middleware

import (
    "github.com/goravel/framework/contracts/http"
    "github.com/goravel/framework/facades"
    "github.com/google/uuid"
    "github.com/macromarkets/vault/app/models"
)

// AccountContext reads X-Account-Id header (JWT sessions) or derives account from
// access token (HMAC). Validates membership and sets account_id + user_role in ctx.
func AccountContext(ctx http.Context) {
    accountIDStr := ctx.Request().Header("X-Account-Id", "")
    if accountIDStr == "" {
        ctx.Request().AbortWithStatus(http.StatusBadRequest)
        ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "X-Account-Id header required"})
        return
    }
    accountID, err := uuid.Parse(accountIDStr)
    if err != nil {
        ctx.Request().AbortWithStatus(http.StatusBadRequest)
        ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid account id"})
        return
    }

    userIDStr, _ := ctx.Value("user_id").(string)
    if userIDStr == "" {
        ctx.Request().Next()
        ctx.WithValue("account_id", accountID)
        return
    }

    userID, _ := uuid.Parse(userIDStr)
    var au models.AccountUser
    err = facades.Orm().Query().
        Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
        First(&au)
    if err != nil {
        ctx.Request().AbortWithStatus(http.StatusForbidden)
        ctx.Response().Json(http.StatusForbidden, http.Json{"error": "not a member of this account"})
        return
    }
    ctx.WithValue("account_id", accountID)
    ctx.WithValue("user_role", au.Role)
    ctx.Request().Next()
}
```

- [ ] **Step 8.4: Implement chain type middleware**

```go
// back/app/http/middleware/chain_type.go
// UTXOOnly — returns 404 if wallet.chain is not btc, ltc, or doge.
// Used on /v1/wallets/:id/unspents and /v1/wallets/:id/consolidate.
var utxoChains = map[string]bool{"btc": true, "ltc": true, "doge": true}

func UTXOOnly(ctx http.Context) {
    walletID := ctx.Request().Input("id")
    var w models.Wallet
    if err := facades.Orm().Query().Where("id = ?", walletID).First(&w); err != nil {
        ctx.Request().AbortWithStatus(http.StatusNotFound)
        ctx.Response().Json(http.StatusNotFound, http.Json{"error": "wallet not found"})
        return
    }
    if !utxoChains[w.Chain] {
        ctx.Request().AbortWithStatus(http.StatusNotFound)
        ctx.Response().Json(http.StatusNotFound, http.Json{"error": "not a UTXO wallet"})
        return
    }
    ctx.WithValue("wallet", w)
    ctx.Request().Next()
}
```

- [ ] **Step 8.5: Build to verify**
```bash
cd back && go build ./...
```

- [ ] **Step 8.6: Commit**
```bash
git add back/app/http/middleware/
git commit -m "feat: add session auth, account context, and chain type middleware"
```

---

## Task 9: Auth Controller

**Files:**
- Create: `back/app/http/controllers/auth_controller.go`
- Create: `back/app/http/controllers/auth_controller_test.go`
- Create: `back/app/http/requests/login_request.go` (+ register, verify_2fa, recover, recover_confirm)

- [ ] **Step 9.1: Write failing tests**

```go
// back/app/http/controllers/auth_controller_test.go
package controllers_test

type AuthControllerTestSuite struct{ suite.Suite; goravelTesting.TestCase }

func TestAuthControllerSuite(t *testing.T) { suite.Run(t, new(AuthControllerTestSuite)) }

func (s *AuthControllerTestSuite) SetupTest() { mocks.TestDB(s.T()) }

func (s *AuthControllerTestSuite) TestRegister_Success() {
    s.Http(s.T()).
        WithHeader("Content-Type", "application/json").
        Post("/auth/register", strings.NewReader(`{"email":"user@test.com","password":"secret123","full_name":"Test User"}`)).
        AssertCreated()
}

func (s *AuthControllerTestSuite) TestLogin_Success() {
    // Register first, then login
    s.Http(s.T()).Post("/auth/register", strings.NewReader(`{"email":"u@t.com","password":"pass123"}`))
    resp, _ := s.Http(s.T()).
        WithHeader("Content-Type", "application/json").
        Post("/auth/login", strings.NewReader(`{"email":"u@t.com","password":"pass123"}`))
    resp.AssertOk().AssertJsonPath("token", func(v any) bool { return v.(string) != "" })
}

func (s *AuthControllerTestSuite) TestLogin_WrongPassword_Returns401() {
    s.Http(s.T()).Post("/auth/register", strings.NewReader(`{"email":"u2@t.com","password":"correct"}`))
    resp, _ := s.Http(s.T()).Post("/auth/login", strings.NewReader(`{"email":"u2@t.com","password":"wrong"}`))
    resp.AssertUnauthorized()
}
```

- [ ] **Step 9.2: Create request structs**

```go
// back/app/http/requests/login_request.go
package requests

type LoginRequest struct {
    Email    string `form:"email" json:"email"`
    Password string `form:"password" json:"password"`
}

func (r *LoginRequest) Rules(ctx http.Context) map[string]string {
    return map[string]string{
        "email":    "required|email",
        "password": "required|min_len:6",
    }
}

func (r *LoginRequest) Authorize(ctx http.Context) error { return nil }
```

Also create:
- `register_request.go`: email(required|email), password(required|min_len:8), full_name(optional)
- `verify_2fa_request.go`: code(required|len:6)
- `recover_request.go`: email(required|email)
- `recover_confirm_request.go`: token(required), password(required|min_len:8), password_confirmation(required|same:password)

- [ ] **Step 9.3: Implement auth controller**

```go
// back/app/http/controllers/auth_controller.go
package controllers

// Register godoc
// @Summary     Register a new user
// @Tags        Auth
// @Accept      json
// @Produce     json
// @Param       body body RegisterRequest true "Registration data"
// @Success     201 {object} map[string]any
// @Failure     422 {object} ErrorResponse
// @Failure     409 {object} ErrorResponse "Email already in use"
// @Router      /auth/register [post]
func Register(ctx http.Context) http.Response {
    var req requests.RegisterRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil {
        if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }
        return ctx.Response().Json(http.StatusUnprocessableEntity, errs.All())
    }
    authSvc := container.Get().AuthService
    hash, err := authSvc.HashPassword(req.Password)
    if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to hash password"}) }

    user := &models.User{ID: uuid.New(), Email: req.Email, PasswordHash: hash, FullName: req.FullName}
    if err := facades.Orm().Query().Create(user); err != nil {
        return ctx.Response().Json(http.StatusConflict, http.Json{"error": "email already in use"})
    }
    // Queue welcome email
    facades.Mail().Queue(mail.NewWelcomeMail(user.Email, user.FullName))
    return ctx.Response().Json(http.StatusCreated, http.Json{"id": user.ID, "email": user.Email})
}

// Login godoc
// @Summary     Login with email and password
// @Tags        Auth
// @Accept      json
// @Produce     json
// @Param       body body LoginRequest true "Credentials"
// @Success     200 {object} map[string]any "Returns token + requires_2fa flag"
// @Failure     401 {object} ErrorResponse
// @Router      /auth/login [post]
func Login(ctx http.Context) http.Response {
    var req requests.LoginRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil {
        if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }
        return ctx.Response().Json(http.StatusUnprocessableEntity, errs.All())
    }
    var user models.User
    if err := facades.Orm().Query().Where("email = ? AND status = 'active'", req.Email).First(&user); err != nil {
        return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
    }
    if !container.Get().AuthService.CheckPassword(req.Password, user.PasswordHash) {
        return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
    }
    if user.TotpEnabled {
        // Issue a short-lived pre-auth token indicating 2FA is required
        return ctx.Response().Json(http.StatusOK, http.Json{"requires_2fa": true, "user_id": user.ID})
    }
    token, err := facades.Auth(ctx).Guard("web").Login(&user)
    if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "login failed"}) }
    refreshToken, _ := issueRefreshToken(ctx, user.ID)
    return ctx.Response().Json(http.StatusOK, http.Json{"token": token, "refresh_token": refreshToken})
}

// Verify2FA, Refresh, Logout, Recover, RecoverConfirm — implement same pattern.
// See spec for full route list.
```

Helper `issueRefreshToken`: generate random token, bcrypt hash it, store in `refresh_tokens` table, return raw token.

- [ ] **Step 9.4: Run tests to verify they pass**
```bash
cd back && go test ./app/http/controllers/ -run TestAuth -v
```
Expected: all PASS.

- [ ] **Step 9.5: Commit**
```bash
git add back/app/http/controllers/auth_controller*.go back/app/http/requests/
git commit -m "feat: add auth controller (register, login, 2FA, refresh, recovery)"
```

---

## Task 10: User Controller

**Files:**
- Create: `back/app/http/controllers/user_controller.go`
- Create: `back/app/http/controllers/user_controller_test.go`

- [ ] **Step 10.1: Write failing tests**

```go
func (s *UserControllerTestSuite) TestGetMe_ReturnsProfile() {
    // register + login to get token
    // GET /v1/user/me with Bearer token
    // assert email, full_name present
}
func (s *UserControllerTestSuite) TestGetMe_NoToken_Returns401() { ... }
func (s *UserControllerTestSuite) TestUpdateMe_ChangesFullName() { ... }
func (s *UserControllerTestSuite) TestChangePassword_Success() { ... }
func (s *UserControllerTestSuite) TestChangePassword_WrongCurrent_Returns401() { ... }
func (s *UserControllerTestSuite) TestGetMyAccounts_ReturnsList() { ... }
```

- [ ] **Step 10.2: Implement user controller**

```go
// back/app/http/controllers/user_controller.go

// GetMe godoc
// @Summary     Get current user profile
// @Tags        Users
// @Security    BearerAuth
// @Produce     json
// @Success     200 {object} models.User
// @Failure     401 {object} ErrorResponse
// @Router      /v1/user/me [get]
func GetMe(ctx http.Context) http.Response {
    userID, _ := ctx.Value("user_id").(string)
    var user models.User
    if err := facades.Orm().Query().Where("id = ?", userID).First(&user); err != nil {
        return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "user not found"})
    }
    return ctx.Response().Json(http.StatusOK, user)
}

// UpdateMe, ChangePassword, GetMyAccounts — same pattern.
// ChangePassword: verify current password, hash new, update, revoke all refresh tokens.
// GetMyAccounts: JOIN account_users + accounts WHERE user_id = ? AND deleted_at IS NULL.
```

- [ ] **Step 10.3: Run tests**
```bash
cd back && go test ./app/http/controllers/ -run TestUser -v
```

- [ ] **Step 10.4: Commit**
```bash
git add back/app/http/controllers/user_controller*.go
git commit -m "feat: add user profile controller"
```

---

## Task 11: Account Controller

**Files:**
- Create: `back/app/http/controllers/account_controller.go`
- Create: `back/app/http/controllers/account_controller_test.go`

- [ ] **Step 11.1: Write failing tests**

```go
func (s *AccountControllerTestSuite) TestCreate_Success() { ... }
func (s *AccountControllerTestSuite) TestGetAccount_Success() { ... }
func (s *AccountControllerTestSuite) TestGetAccount_WrongUser_Returns403() { ... } // isolation test
func (s *AccountControllerTestSuite) TestFreezeAccount_RequiresOwner() { ... }
func (s *AccountControllerTestSuite) TestListUsers_Success() { ... }
func (s *AccountControllerTestSuite) TestAddUser_Success() { ... }
func (s *AccountControllerTestSuite) TestRemoveUser_SoftDeletes() { ... }
func (s *AccountControllerTestSuite) TestCreateToken_Success() { ... }
func (s *AccountControllerTestSuite) TestRemoveToken_Success() { ... }
```

- [ ] **Step 11.2: Implement account controller**

```go
// back/app/http/controllers/account_controller.go

// CreateAccount godoc
// @Summary     Create a new account
// @Tags        Accounts
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Param       body body CreateAccountRequest true "Account data"
// @Success     201 {object} models.Account
// @Router      /v1/accounts [post]
func CreateAccount(ctx http.Context) http.Response {
    var req requests.CreateAccountRequest
    if errs, err := ctx.Request().ValidateRequest(&req); err != nil || errs != nil { ... }

    userID, _ := uuid.Parse(ctx.Value("user_id").(string))
    acc, err := container.Get().AccountService.Create(ctx.Context(), req.Name, userID)
    if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }
    return ctx.Response().Json(http.StatusCreated, acc)
}

// Implement: ListAccounts, GetAccount, UpdateAccount, FreezeAccount, ArchiveAccount,
// ListAccountUsers, AddAccountUser, RemoveAccountUser, ListAccessTokens,
// CreateAccessToken, RemoveAccessToken.
//
// Pattern: check gates via facades.Gate().WithContext(ctx.Context()).Denies("account:admin", args)
// before any mutating operation.
```

- [ ] **Step 11.3: Run tests**
```bash
cd back && go test ./app/http/controllers/ -run TestAccount -v
```

- [ ] **Step 11.4: Commit**
```bash
git add back/app/http/controllers/account_controller*.go
git commit -m "feat: add account management controller"
```

---

## Task 12: Wire Routes + Container

**Files:** Modify `back/routes/api.go`, `back/app/container/container.go`

- [ ] **Step 12.1: Add AuthService and AccountService to container**

```go
// back/app/container/container.go — add to Container struct:
AuthService    *authsvc.Service
AccountService *accountsvc.Service

// In Boot():
c.AuthService = authsvc.NewService()
c.AccountService = accountsvc.NewService()
```

- [ ] **Step 12.2: Add all new routes to `routes/api.go`**

```go
// Auth routes (no middleware)
facades.Route().Prefix("/auth").Group(func(router route.Router) {
    router.Post("/register", controllers.Register)
    router.Post("/login", controllers.Login)
    router.Post("/refresh", controllers.RefreshToken)
    router.Post("/logout", controllers.Logout)
    router.Post("/2fa/verify", controllers.Verify2FA)
    router.Post("/2fa/setup", controllers.Setup2FA)
    router.Post("/2fa/confirm", controllers.Confirm2FA)
    router.Delete("/2fa", controllers.Disable2FA)
    router.Get("/2fa/recovery-codes", controllers.GetRecoveryCodes)
    router.Post("/2fa/recovery-codes/use", controllers.UseRecoveryCode)
    router.Post("/recover", controllers.RecoverPassword)
    router.Post("/recover/confirm", controllers.RecoverPasswordConfirm)
})

// User profile (session JWT required)
facades.Route().Prefix("/v1/user").
    Middleware(middleware.SessionAuth).
    Group(func(router route.Router) {
        router.Get("/me", controllers.GetMe)
        router.Put("/me", controllers.UpdateMe)
        router.Post("/me/password", controllers.ChangePassword)
        router.Get("/me/accounts", controllers.GetMyAccounts)
    })

// Accounts (session JWT + account context)
facades.Route().Prefix("/v1/accounts").
    Middleware(middleware.SessionAuth).
    Group(func(router route.Router) {
        router.Get("/", controllers.ListAccounts)
        router.Post("/", controllers.CreateAccount)
        router.Prefix("/{id}").
            Middleware(middleware.AccountContext).
            Group(func(r route.Router) {
                r.Get("/", controllers.GetAccount)
                r.Put("/", controllers.UpdateAccount)
                r.Post("/freeze", controllers.FreezeAccount)
                r.Post("/archive", controllers.ArchiveAccount)
                r.Get("/users", controllers.ListAccountUsers)
                r.Post("/users", controllers.AddAccountUser)
                r.Delete("/users/{userId}", controllers.RemoveAccountUser)
                r.Get("/tokens", controllers.ListAccessTokens)
                r.Post("/tokens", controllers.CreateAccessToken)
                r.Delete("/tokens/{tokenId}", controllers.RemoveAccessToken)
            })
    })

// Existing /v1 routes — add SessionAuth as alternative to HMACAuth
// Both middlewares populate compatible context values.
// For now, keep existing HMAC-only routes unchanged; new routes use SessionAuth.
```

- [ ] **Step 12.3: Build**
```bash
cd back && go build ./...
```
Expected: no errors.

- [ ] **Step 12.4: Run all tests**
```bash
cd back && go test ./... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 12.5: Commit**
```bash
git add back/routes/ back/app/container/
git commit -m "feat: wire auth, user, and account routes"
```

---

## Task 13: Swagger Annotations

**Files:** All new controllers

- [ ] **Step 13.1: Add Swagger annotations to every new endpoint** — follow existing pattern from `wallets_controller.go`. Every endpoint needs `@Summary`, `@Tags`, `@Security`, `@Param`, `@Success`, `@Failure`, `@Router`.

- [ ] **Step 13.2: Regenerate Swagger docs**
```bash
cd back && make swagger-generate
```
Expected: `docs/swagger.json` updated, no errors.

- [ ] **Step 13.3: Verify in Swagger UI**
```bash
cd back && make run
# Open http://localhost:8080/swagger/index.html
# Verify Auth, Users, Accounts tags all appear with correct endpoints
```

- [ ] **Step 13.4: Commit**
```bash
git add back/docs/ back/app/http/controllers/
git commit -m "docs: add Swagger annotations for auth, user, and account endpoints"
```

---

## Task 14: Final Verification

- [ ] **Step 14.1: Run full test suite**
```bash
cd back && go test ./... -count=1 2>&1 | grep -E "PASS|FAIL|panic"
```
Expected: all PASS, no panics.

- [ ] **Step 14.2: Run linter**
```bash
cd back && make lint
```

- [ ] **Step 14.3: Tag completion commit**
```bash
git commit --allow-empty -m "feat: backend foundation complete (auth + accounts + users)"
```
