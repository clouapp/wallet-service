# MPC Wallet KeyCard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the placeholder `AdminCreateWallet` controller with the full MPC wallet creation flow, including LocalStack-backed Secrets Manager, a KeyCard PDF download, and a 6-digit activation code step.

**Architecture:** Backend runs real MPC keygen and stores ShareB in AWS Secrets Manager (LocalStack in dev). A new `CreateWalletResult` type carries keycard data back to the frontend, which generates and downloads a PDF. The wallet starts `pending` and becomes `active` only after the user enters the activation code from the PDF.

**Tech Stack:** Go 1.22 / Goravel v1.17, aws-sdk-go-v2, Next.js (pages router), `@react-pdf/renderer`, `qrcode` npm package.

**Spec:** `docs/superpowers/specs/2026-03-24-mpc-wallet-keycard-design.md`

---

## File Map

| Action | Path | Purpose |
|--------|------|---------|
| Modify | `app/services/mpc/keystore.go` | Add `EncryptWithServiceKey` |
| Create | `database/migrations/20260324000015_alter_wallets_add_keycard_fields.go` | Add `activation_code` col, change status default |
| Modify | `database/migrations/migrations.go` | Register new migration |
| Modify | `app/models/wallet.go` | Add `ActivationCode *string` field |
| Modify | `app/services/wallet/service.go` | `CreateWalletResult`, chain normalisation, `ActivateWallet` |
| Modify | `app/services/wallet/service_test.go` | Update for new return type, add activate tests |
| Modify | `app/container/container.go` | LocalStack endpoint override |
| Modify | `app/http/controllers/wallets_controller.go` | Remove placeholder, add `CreateWalletAdmin` + `ActivateWallet` |
| Modify | `routes/api.go` | Wire new routes, remove old `AdminCreateWallet` |
| Modify | `.env` / `.env.dev` | Add `WALLET_SERVICE_KEY`, LocalStack vars |
| Modify | `front/package.json` | Add `@react-pdf/renderer`, `qrcode`, `@types/qrcode` |
| Create | `front/src/lib/keycard/KeycardDocument.tsx` | `@react-pdf/renderer` PDF component |
| Create | `front/src/lib/keycard/generateKeycardPdf.ts` | Generate blob + trigger browser download |
| Modify | `front/src/components/Modals/CreateWalletModal.tsx` | Two-step flow (form → keycard) |

---

## Task 1: `mpc.EncryptWithServiceKey` helper

**Files:**
- Modify: `app/services/mpc/keystore.go`

- [ ] **Step 1: Write the failing test**

Add to a new file `app/services/mpc/keystore_service_key_test.go`:

```go
package mpc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 64 hex chars = 32 bytes
const testServiceKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptWithServiceKey_ReturnsValidJSON(t *testing.T) {
	result, err := EncryptWithServiceKey([]byte("my-secret-passphrase"), testServiceKey)
	require.NoError(t, err)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	assert.NotEmpty(t, parsed["iv"])
	assert.NotEmpty(t, parsed["ct"])
	assert.Equal(t, "aes-256-gcm", parsed["cipher"])
	// should NOT have salt or kdf fields
	assert.Empty(t, parsed["salt"])
	assert.Empty(t, parsed["kdf"])
}

func TestEncryptWithServiceKey_DifferentNonceEachCall(t *testing.T) {
	data := []byte("passphrase")
	r1, err1 := EncryptWithServiceKey(data, testServiceKey)
	r2, err2 := EncryptWithServiceKey(data, testServiceKey)
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, r1, r2, "each call should use a fresh random nonce")
}

func TestEncryptWithServiceKey_InvalidKeyHex(t *testing.T) {
	_, err := EncryptWithServiceKey([]byte("data"), "not-valid-hex")
	assert.ErrorContains(t, err, "decode key")
}

func TestEncryptWithServiceKey_WrongKeyLength(t *testing.T) {
	// 32 hex chars = 16 bytes, not 32
	_, err := EncryptWithServiceKey([]byte("data"), "0102030405060708090a0b0c0d0e0f10")
	assert.ErrorContains(t, err, "must be 32 bytes")
}
```

- [ ] **Step 2: Run test — expect compile failure (function doesn't exist yet)**

```bash
cd /path/to/back && go test ./app/services/mpc/... -run TestEncryptWithServiceKey -v
```
Expected: build error — `EncryptWithServiceKey undefined`

- [ ] **Step 3: Implement `EncryptWithServiceKey` in `app/services/mpc/keystore.go`**

Add after the existing `DecryptShare` function:

```go
// EncryptWithServiceKey encrypts data with a service-held AES-256-GCM key.
// keyHex must be a 64-character hex string (32 bytes, high-entropy machine key).
// No KDF is applied. Returns JSON: {"iv":"<b64>","ct":"<b64>","cipher":"aes-256-gcm"}.
func EncryptWithServiceKey(data []byte, keyHex string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("service key must be 32 bytes, got %d", len(key))
	}

	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	ct := gcm.Seal(nil, nonce, data, nil)

	type payload struct {
		IV     string `json:"iv"`
		CT     string `json:"ct"`
		Cipher string `json:"cipher"`
	}
	p := payload{
		IV:     base64.StdEncoding.EncodeToString(nonce),
		CT:     base64.StdEncoding.EncodeToString(ct),
		Cipher: "aes-256-gcm",
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

Add missing imports to the file: `"crypto/aes"`, `"crypto/cipher"`, `"encoding/base64"`, `"encoding/hex"`, `"encoding/json"`, `"fmt"`, `"io"`. The file already imports `"crypto/rand"` — keep it.

- [ ] **Step 4: Run tests — expect all pass**

```bash
cd /path/to/back && go test ./app/services/mpc/... -run TestEncryptWithServiceKey -v
```
Expected: 4 tests PASS

- [ ] **Step 5: Build check**

```bash
cd /path/to/back && go build ./...
```
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add app/services/mpc/keystore.go app/services/mpc/keystore_service_key_test.go
git commit -m "feat: add mpc.EncryptWithServiceKey for KeyCard section D"
```

---

## Task 2: DB migration + model

**Files:**
- Create: `database/migrations/20260324000015_alter_wallets_add_keycard_fields.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/wallet.go`

- [ ] **Step 1: Create migration file**

Create `database/migrations/20260324000015_alter_wallets_add_keycard_fields.go`:

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260324000015AlterWalletsAddKeycardFields struct{}

func (r *M20260324000015AlterWalletsAddKeycardFields) Signature() string {
	return "20260324000015_alter_wallets_add_keycard_fields"
}

func (r *M20260324000015AlterWalletsAddKeycardFields) Up() error {
	if err := facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.String("activation_code", 6).Nullable()
	}); err != nil {
		return err
	}
	// Goravel Blueprint doesn't expose ChangeDefault — use raw SQL
	return facades.Orm().Query().Exec(
		"ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'pending'",
	)
}

func (r *M20260324000015AlterWalletsAddKeycardFields) Down() error {
	facades.Orm().Query().Exec(
		"ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'active'",
	)
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.DropColumn("activation_code")
	})
}
```

- [ ] **Step 2: Register migration in `database/migrations/migrations.go`**

Add `&M20260324000015AlterWalletsAddKeycardFields{}` as the last entry in the `All()` slice.

- [ ] **Step 3: Add `ActivationCode` field to `app/models/wallet.go`**

Add after `FrozenUntil`:

```go
ActivationCode *string `gorm:"type:char(6)" json:"-"`
```

- [ ] **Step 4: Build check**

```bash
cd /path/to/back && go build ./...
```
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add database/migrations/20260324000015_alter_wallets_add_keycard_fields.go \
        database/migrations/migrations.go \
        app/models/wallet.go
git commit -m "feat: add activation_code column and pending status default to wallets"
```

---

## Task 3: Wallet service — `CreateWalletResult` + updated `CreateWallet`

**Files:**
- Modify: `app/services/wallet/service.go`

- [ ] **Step 1: Write failing tests for the new return type**

Add to `app/services/wallet/service_test.go` in `WalletServiceTestSuite`:

```go
func (s *WalletServiceTestSuite) TestCreateWallet_Success_ReturnsKeycardData() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "eth", "Ethereum Wallet", testPassphrase)
	s.Require().NoError(err)

	// Wallet fields
	s.Equal("eth", result.Wallet.Chain)
	s.Equal("Ethereum Wallet", result.Wallet.Label)
	s.Equal("pending", result.Wallet.Status)
	s.NotEmpty(result.Wallet.DepositAddress)
	s.NotEmpty(result.Wallet.MPCPublicKey)
	s.NotEmpty(result.Wallet.MPCCustomerShare)
	s.NotEmpty(result.Wallet.MPCSecretARN)
	s.Equal("secp256k1", result.Wallet.MPCCurve)
	s.NotNil(result.Wallet.ActivationCode)
	s.Len(*result.Wallet.ActivationCode, 6)

	// Keycard fields
	s.NotEmpty(result.EncryptedUserKey)
	s.NotEmpty(result.ServicePublicKey)
	s.NotEmpty(result.EncryptedPasscode)
	s.NotEmpty(result.ActivationCode)
	s.Len(result.ActivationCode, 6)
	s.Regexp(`^\d{6}$`, result.ActivationCode)
}

func (s *WalletServiceTestSuite) TestCreateWallet_NormalisesChainID() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "ETH", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("eth", result.Wallet.Chain)
}

func (s *WalletServiceTestSuite) TestCreateWallet_MATICNormalisedToPolygon() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	// register polygon chain
	s.registry.RegisterChain(mocks.NewMockChain("polygon"))
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "MATIC", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("polygon", result.Wallet.Chain)
}
```

Also add `"os"` and `"regexp"` to imports if needed (testify's `s.Regexp` handles the regexp check via the suite).

- [ ] **Step 2: Update existing tests that reference `w.` directly**

In `TestCreateWallet_Success` (old test around line 101), change to use `result.Wallet.*`:

```go
func (s *WalletServiceTestSuite) TestCreateWallet_Success() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "eth", "Ethereum Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("eth", result.Wallet.Chain)
	s.Equal("Ethereum Wallet", result.Wallet.Label)
	s.NotEmpty(result.Wallet.DepositAddress)
	s.NotEmpty(result.Wallet.MPCPublicKey)
	s.NotEmpty(result.Wallet.MPCCustomerShare)
	s.NotEmpty(result.Wallet.MPCSecretARN)
	s.Equal("secp256k1", result.Wallet.MPCCurve)
}
```

Update `TestCreateWallet_SolanaUsesEd25519`:

```go
func (s *WalletServiceTestSuite) TestCreateWallet_SolanaUsesEd25519() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "sol", "Solana Wallet", testPassphrase)
	s.Require().NoError(err)
	s.Equal("sol", result.Wallet.Chain)
	s.Equal("ed25519", result.Wallet.MPCCurve)
	s.NotEmpty(result.Wallet.DepositAddress)
}
```

Update `TestCreateWallet_DuplicateChain`:

```go
func (s *WalletServiceTestSuite) TestCreateWallet_DuplicateChain() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	_, err := s.service.CreateWallet(ctx, "eth", "First", testPassphrase)
	s.Require().NoError(err)

	_, err = s.service.CreateWallet(ctx, "eth", "Second", testPassphrase)
	s.Error(err)
	s.Contains(err.Error(), "already exists")
}
```

Unit tests `TestCreateWallet_PassphraseTooShort` and `TestCreateWallet_UnknownChain` don't access the return value fields, just `err` — they stay as-is but the `_` variable now receives `*CreateWalletResult`. No change needed.

- [ ] **Step 3: Run tests — expect compile errors**

```bash
cd /path/to/back && go test ./app/services/wallet/... -v 2>&1 | head -30
```
Expected: build errors about `CreateWalletResult` undefined, `w.Chain` not on `*models.Wallet`.

- [ ] **Step 4: Implement changes in `app/services/wallet/service.go`**

**4a.** Add imports: `cryptorand "crypto/rand"` (named import — all usages in this file use `cryptorand.Int(cryptorand.Reader, ...)` to avoid ambiguity), `"crypto/subtle"`, `"encoding/base64"`, `"encoding/json"`, `"errors"`, `"log/slog"`, `"math/big"`, `"strings"`.

The file already imports `"crypto/rand"` (used by `rand.Read` in keygen.go) — actually `service.go` itself doesn't import it. Add `cryptorand "crypto/rand"` as a named import to avoid clash with `math/rand` if needed, or just `"crypto/rand"` since we're only using `crypto/rand`.

**4b.** Add sentinel errors and result type after the `import` block:

```go
var (
	ErrWalletNotFound        = errors.New("wallet not found")
	ErrWalletAlreadyActive   = errors.New("wallet is not pending activation")
	ErrInvalidActivationCode = errors.New("invalid activation code")
)

// CreateWalletResult holds the wallet record plus one-time KeyCard data.
type CreateWalletResult struct {
	Wallet            *models.Wallet
	EncryptedUserKey  string // JSON {iv,salt,ct,cipher,kdf} — AES-256-GCM/Argon2id, base64
	ServicePublicKey  string // hex of CombinedPubKey
	EncryptedPasscode string // JSON {iv,ct,cipher} — AES-256-GCM with service key, base64
	ActivationCode    string // 6-digit zero-padded decimal
}
```

**4c.** Replace the `CreateWallet` function signature and body:

```go
func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*CreateWalletResult, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}

	// Normalise chain ID
	chainID = strings.ToLower(chainID)
	if chainID == "matic" {
		chainID = "polygon"
	}

	if _, err := s.registry.Chain(chainID); err != nil {
		return nil, fmt.Errorf("unknown chain: %s", chainID)
	}

	// Guard: one wallet per chain
	var count int64
	count, err := facades.Orm().Query().Model(&models.Wallet{}).Where("chain", chainID).Count()
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("wallet for chain %s already exists", chainID)
	}

	curve := curveForChain(chainID)

	// 1. MPC keygen
	result, err := s.mpcService.Keygen(ctx, curve)
	if err != nil {
		return nil, fmt.Errorf("mpc keygen: %w", err)
	}

	// 2. Encrypt customer share (ShareA) with passphrase
	enc, err := mpc.EncryptShare(result.ShareA, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt share: %w", err)
	}

	// 3. Format EncryptedUserKey JSON
	type userKeyPayload struct {
		IV     string `json:"iv"`
		Salt   string `json:"salt"`
		CT     string `json:"ct"`
		Cipher string `json:"cipher"`
		KDF    string `json:"kdf"`
	}
	ukp := userKeyPayload{
		IV:     base64.StdEncoding.EncodeToString(enc.IV),
		Salt:   base64.StdEncoding.EncodeToString(enc.Salt),
		CT:     base64.StdEncoding.EncodeToString(enc.Ciphertext),
		Cipher: "aes-256-gcm",
		KDF:    "argon2id",
	}
	ukJSON, err := json.Marshal(ukp)
	if err != nil {
		return nil, fmt.Errorf("marshal user key: %w", err)
	}

	// 4. Encrypt passphrase with service key (section D)
	encPasscode, err := mpc.EncryptWithServiceKey([]byte(passphrase), os.Getenv("WALLET_SERVICE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("encrypt passcode: %w", err)
	}

	// 5. Generate 6-digit activation code
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, fmt.Errorf("generate activation code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	// 6. Store service share (ShareB) in Secrets Manager
	walletID := uuid.New()
	secretName := fmt.Sprintf("vault/wallet/%s/share-b", walletID.String())
	out, err := s.secretsManager.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		SecretBinary: result.ShareB,
	})
	if err != nil {
		return nil, fmt.Errorf("store service share: %w", err)
	}
	secretARN := aws.ToString(out.ARN)

	// All steps after CreateSecret: log orphaned ARN on any failure
	onPostSecretErr := func(err error) (*CreateWalletResult, error) {
		slog.Warn("orphaned secret ARN after wallet creation failure", "arn", secretARN, "error", err)
		return nil, err
	}

	// 7. Derive deposit address
	depositAddress, err := deriveAddress(chainID, result.CombinedPubKey)
	if err != nil {
		return onPostSecretErr(fmt.Errorf("derive address: %w", err))
	}

	// 8. Persist wallet
	codeStr := code
	w := &models.Wallet{
		ID:               walletID,
		Chain:            chainID,
		Label:            label,
		MPCCustomerShare: hex.EncodeToString(enc.Ciphertext),
		MPCShareIV:       hex.EncodeToString(enc.IV),
		MPCShareSalt:     hex.EncodeToString(enc.Salt),
		MPCSecretARN:     secretARN,
		MPCPublicKey:     hex.EncodeToString(result.CombinedPubKey),
		MPCCurve:         string(curve),
		DepositAddress:   depositAddress,
		Status:           "pending",
		ActivationCode:   &codeStr,
	}
	if err := facades.Orm().Query().Create(w); err != nil {
		return onPostSecretErr(fmt.Errorf("create wallet: %w", err))
	}

	// 9. Cache deposit address in Redis (non-fatal)
	if s.rdb != nil {
		if err := s.rdb.SAdd(ctx, "vault:addresses:"+chainID, depositAddress).Err(); err != nil {
			slog.Warn("redis cache failed", "error", err)
		}
	}

	return &CreateWalletResult{
		Wallet:            w,
		EncryptedUserKey:  string(ukJSON),
		ServicePublicKey:  hex.EncodeToString(result.CombinedPubKey),
		EncryptedPasscode: encPasscode,
		ActivationCode:    code,
	}, nil
}
```

Note: add `"crypto/rand"` import as `rand` (remove old `rand` reference if any clash), add `"math/big"`. The file already imports `"encoding/hex"` and `"github.com/aws/aws-sdk-go-v2/aws"` and `"github.com/aws/aws-sdk-go-v2/service/secretsmanager"` and `"github.com/google/uuid"`. Add: `"crypto/rand"`, `"encoding/base64"`, `"encoding/json"`, `"errors"`, `"log/slog"`, `"math/big"`, `"strings"`.

- [ ] **Step 5: Run tests — expect pass**

```bash
cd /path/to/back && go test ./app/services/wallet/... -v -run "TestWalletServiceSuite/TestCreateWallet" 2>&1
```
Expected: all TestCreateWallet_* tests PASS (build may fail first on ActivateWallet tests — add those in Task 4)

- [ ] **Step 6: Build check**

```bash
cd /path/to/back && go build ./... 2>&1
```
Expected: errors in `wallets_controller.go` (caller now gets `*CreateWalletResult`) — that's fine, fixed in Task 7.

- [ ] **Step 7: Commit**

```bash
git add app/services/wallet/service.go app/services/wallet/service_test.go
git commit -m "feat: CreateWallet returns CreateWalletResult with keycard data"
```

---

## Task 4: Wallet service — `ActivateWallet`

**Files:**
- Modify: `app/services/wallet/service.go`
- Modify: `app/services/wallet/service_test.go`

- [ ] **Step 1: Write failing tests**

Add to `WalletServiceTestSuite` in `service_test.go`:

```go
func (s *WalletServiceTestSuite) TestActivateWallet_Success() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "eth", "Test", testPassphrase)
	s.Require().NoError(err)
	s.Equal("pending", result.Wallet.Status)

	activated, err := s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.Require().NoError(err)
	s.Equal("active", activated.Status)
	s.Nil(activated.ActivationCode)
}

func (s *WalletServiceTestSuite) TestActivateWallet_WrongCode() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "eth", "Test", testPassphrase)
	s.Require().NoError(err)

	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, "000000")
	s.ErrorIs(err, ErrInvalidActivationCode)
}

func (s *WalletServiceTestSuite) TestActivateWallet_AlreadyActive() {
	os.Setenv("WALLET_SERVICE_KEY", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	ctx := context.Background()

	result, err := s.service.CreateWallet(ctx, "eth", "Test", testPassphrase)
	s.Require().NoError(err)

	// Activate once
	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.Require().NoError(err)

	// Activate again — expect error
	_, err = s.service.ActivateWallet(ctx, result.Wallet.ID, result.ActivationCode)
	s.ErrorIs(err, ErrWalletAlreadyActive)
}

func (s *WalletServiceTestSuite) TestActivateWallet_NotFound() {
	ctx := context.Background()
	_, err := s.service.ActivateWallet(ctx, uuid.New(), "123456")
	s.ErrorIs(err, ErrWalletNotFound)
}
```

Add `"github.com/google/uuid"` to test file imports if not already present.

- [ ] **Step 2: Run tests — expect compile error**

```bash
cd /path/to/back && go test ./app/services/wallet/... -run "TestActivateWallet" -v 2>&1 | head -20
```
Expected: `ActivateWallet undefined`

- [ ] **Step 3: Implement `ActivateWallet` in `service.go`**

Add after `CreateWallet`:

```go
// ActivateWallet validates the activation code and transitions the wallet to active.
func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error) {
	var w models.Wallet
	if err := facades.Orm().Query().Where("id", walletID).First(&w); err != nil {
		return nil, ErrWalletNotFound
	}
	if w.ID == uuid.Nil {
		return nil, ErrWalletNotFound
	}
	if w.Status != "pending" {
		return nil, ErrWalletAlreadyActive
	}
	if w.ActivationCode == nil {
		return nil, ErrWalletNotFound
	}
	if subtle.ConstantTimeCompare([]byte(*w.ActivationCode), []byte(code)) != 1 {
		return nil, ErrInvalidActivationCode
	}

	if err := facades.Orm().Query().Model(&w).Updates(map[string]any{
		"status":          "active",
		"activation_code": nil,
	}); err != nil {
		return nil, fmt.Errorf("activate wallet: %w", err)
	}
	w.Status = "active"
	w.ActivationCode = nil
	return &w, nil
}
```

Add `"crypto/subtle"` to imports.

- [ ] **Step 4: Run tests — expect all pass**

```bash
cd /path/to/back && go test ./app/services/wallet/... -v 2>&1
```
Expected: all wallet tests PASS

- [ ] **Step 5: Commit**

```bash
git add app/services/wallet/service.go app/services/wallet/service_test.go
git commit -m "feat: add ActivateWallet service method with timing-safe code comparison"
```

---

## Task 5: LocalStack endpoint wiring

**Files:**
- Modify: `app/container/container.go`
- Modify: `.env` (and `.env.dev` if it exists separately)

- [ ] **Step 1: Add `staticEndpointResolver` + conditional wiring in `app/container/container.go`**

After the `import` block, add a private type (below the struct definition but before `Boot`):

```go
// staticEndpointResolver routes all Secrets Manager calls to a fixed endpoint.
// Used in dev to point at LocalStack.
type staticEndpointResolver struct{ url string }

func (r staticEndpointResolver) ResolveEndpoint(
	ctx context.Context,
	params secretsmanager.EndpointParameters,
) (smithyendpoints.Endpoint, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{URI: *u}, nil
}
```

Required new imports:
```go
"net/url"
smithyendpoints "github.com/aws/smithy-go/endpoints"
```

(`github.com/aws/smithy-go` is already an indirect dependency of `aws-sdk-go-v2` — verify with `go list -m github.com/aws/smithy-go`.)

In `Boot()`, replace the single `smClient := secretsmanager.NewFromConfig(awsCfg)` line with:

```go
smClient := secretsmanager.NewFromConfig(awsCfg)
if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
	smClient = secretsmanager.NewFromConfig(awsCfg,
		secretsmanager.WithEndpointResolverV2(staticEndpointResolver{url: endpoint}))
}
c.SecretsManager = smClient
```

Remove the old `smClient := secretsmanager.NewFromConfig(awsCfg)` line that was setting `c.SecretsManager` further down.

- [ ] **Step 2: Add env vars to `.env`**

Ensure `.env` contains (add if absent):

```
WALLET_SERVICE_KEY=0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20
AWS_ENDPOINT_URL=http://localhost:4566
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
AWS_DEFAULT_REGION=us-east-1
```

The `WALLET_SERVICE_KEY` value above is a dev-only test key. In production use a real random 32-byte hex secret.

- [ ] **Step 3: Build check**

```bash
cd /path/to/back && go build ./... 2>&1
```
Expected: builds (controller errors may still exist from Task 3 — that's OK until Task 6).

- [ ] **Step 4: Commit**

```bash
git add app/container/container.go .env .env.dev
git commit -m "feat: add LocalStack endpoint override for Secrets Manager in dev"
```

---

## Task 6: Backend controllers + routes

**Files:**
- Modify: `app/http/controllers/wallets_controller.go`
- Modify: `routes/api.go`

- [ ] **Step 1: Replace `AdminCreateWallet` and update external `CreateWallet` in `wallets_controller.go`**

**Remove** the `AdminCreateWallet` function (lines ~95–137 in current file) and the `AdminCreateWalletRequest` struct.

**Update** the external `CreateWallet` function to handle new return type. Change:
```go
w, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
if err != nil { ... }
return ctx.Response().Json(http.StatusCreated, w)
```
To:
```go
result, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
if err != nil {
    return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
}
return ctx.Response().Json(http.StatusCreated, result.Wallet)
```

**Add** the two new functions after `GetWallet`:

```go
// CreateWalletAdmin creates a wallet from the admin panel with full MPC keygen.
// Returns keycard data including activation_code for the two-step setup flow.
func CreateWalletAdmin(ctx http.Context) http.Response {
	var req struct {
		Chain             string `json:"chain"`
		Label             string `json:"label"`
		Passphrase        string `json:"passphrase"`
		ConfirmPassphrase string `json:"confirm_passphrase"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Chain == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "chain is required"})
	}
	if req.Label == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "label is required"})
	}
	if len(req.Passphrase) < 12 {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "passphrase must be at least 12 characters"})
	}
	if req.Passphrase != req.ConfirmPassphrase {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "passphrases do not match"})
	}

	result, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown chain") {
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": msg})
		}
		if strings.Contains(msg, "already exists") {
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": msg})
		}
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": msg})
	}

	return ctx.Response().Json(http.StatusCreated, http.Json{
		"wallet":            result.Wallet,
		"encrypted_user_key": result.EncryptedUserKey,
		"service_public_key": result.ServicePublicKey,
		"encrypted_passcode": result.EncryptedPasscode,
		"activation_code":    result.ActivationCode,
	})
}

// ActivateWallet confirms the user has saved their KeyCard by validating the activation code.
func ActivateWallet(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	_, err = container.Get().WalletService.ActivateWallet(ctx.Context(), walletID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, wallet.ErrWalletNotFound):
			return ctx.Response().Json(http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrWalletAlreadyActive):
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrInvalidActivationCode):
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
		default:
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"status": "active"})
}
```

Add imports: `"errors"`, `walletpkg "github.com/macromarkets/vault/app/services/wallet"`. Remove the now-unused `facades` import if `AdminCreateWallet` was its only user — but we just removed that function. Check if `facades` is still needed; if not, remove it. The `var _ models.Wallet` at the bottom keeps the models import — leave it.

- [ ] **Step 2: Update `routes/api.go`**

In the `/v1/wallets` `SessionAuth` group:

Remove:
```go
router.Post("", controllers.AdminCreateWallet)
```

Add:
```go
router.Post("", controllers.CreateWalletAdmin)
```

Inside the nested `/{walletId}` group (alongside existing `r.Get("")`, `r.Patch("")`, etc.), add:
```go
r.Post("/activate", controllers.ActivateWallet)
```

- [ ] **Step 3: Build check**

```bash
cd /path/to/back && go build ./... 2>&1
```
Expected: clean build — no errors

- [ ] **Step 4: Commit**

```bash
git add app/http/controllers/wallets_controller.go routes/api.go
git commit -m "feat: add CreateWalletAdmin and ActivateWallet controllers"
```

---

## Task 7: Frontend — install dependencies

**Files:**
- Modify: `front/package.json`

- [ ] **Step 1: Install new packages**

```bash
cd /path/to/front && npm install @react-pdf/renderer qrcode
npm install --save-dev @types/qrcode
```

- [ ] **Step 2: Verify build still works**

```bash
cd /path/to/front && npm run build 2>&1 | tail -20
```
Expected: no new errors from added packages

- [ ] **Step 3: Commit**

```bash
cd /path/to/front
git add package.json package-lock.json
git commit -m "feat: add @react-pdf/renderer and qrcode dependencies for KeyCard PDF"
```

---

## Task 8: Frontend — `KeycardDocument` + `generateKeycardPdf`

**Files:**
- Create: `front/src/lib/keycard/KeycardDocument.tsx`
- Create: `front/src/lib/keycard/generateKeycardPdf.ts`

The `@react-pdf/renderer` must only run client-side. Both files use dynamic import to avoid SSR issues.

- [ ] **Step 1: Create `front/src/lib/keycard/KeycardDocument.tsx`**

```tsx
// KeycardDocument.tsx — React PDF component. Import only from generateKeycardPdf.ts.
import {
  Document,
  Page,
  View,
  Text,
  Image,
  StyleSheet,
} from '@react-pdf/renderer'

export interface KeycardData {
  walletName: string
  chain: string        // display value e.g. "ETH"
  createdAt: string    // e.g. "Tue Mar 24 2026"
  activationCode: string
  encryptedUserKey: string
  servicePublicKey: string
  encryptedPasscode: string
}

interface Props {
  data: KeycardData
  qrUserKey: string | null
  qrServicePubKey: string | null
  qrPasscode: string | null
}

const styles = StyleSheet.create({
  page: { padding: 40, fontFamily: 'Helvetica', fontSize: 10 },
  header: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 },
  logo: { fontSize: 16, fontFamily: 'Helvetica-Bold' },
  chainLabel: { fontSize: 14 },
  keycardLabel: { fontSize: 14 },
  activationBox: { border: '1pt solid #000', padding: '4pt 8pt' },
  activationTitle: { fontSize: 8, color: '#666' },
  activationCode: { fontSize: 20, fontFamily: 'Helvetica-Bold' },
  warningBanner: {
    backgroundColor: '#fff0f0',
    border: '1pt solid #cc0000',
    padding: 8,
    marginBottom: 12,
    color: '#cc0000',
    textAlign: 'center',
    fontSize: 9,
  },
  walletMeta: { marginBottom: 16 },
  sectionTitle: { fontSize: 11, fontFamily: 'Helvetica-Bold', marginBottom: 2 },
  sectionDesc: { color: '#444', marginBottom: 6 },
  sectionBody: { flexDirection: 'row', gap: 12, marginBottom: 20 },
  dataBlock: {
    flex: 1,
    fontFamily: 'Courier',
    fontSize: 7,
    backgroundColor: '#f5f5f5',
    padding: 6,
    wordBreak: 'break-all',
  },
  qrImage: { width: 80, height: 80 },
  qrUnavailable: { width: 80, height: 80, backgroundColor: '#eee', justifyContent: 'center', alignItems: 'center' },
  qrUnavailableText: { fontSize: 7, color: '#999', textAlign: 'center' },
  faqTitle: { fontSize: 16, fontFamily: 'Helvetica-Bold', marginBottom: 16 },
  faqQ: { fontFamily: 'Helvetica-Bold', marginTop: 10, marginBottom: 2 },
  faqA: { color: '#333' },
})

function QRSlot({ dataUrl }: { dataUrl: string | null }) {
  if (dataUrl) return <Image src={dataUrl} style={styles.qrImage} />
  return (
    <View style={styles.qrUnavailable}>
      <Text style={styles.qrUnavailableText}>— QR unavailable —</Text>
    </View>
  )
}

function Section({
  label, description, data, qr,
}: { label: string; description: string; data: string; qr: string | null }) {
  return (
    <View>
      <Text style={styles.sectionTitle}>{label}</Text>
      <Text style={styles.sectionDesc}>{description}</Text>
      <View style={styles.sectionBody}>
        <QRSlot dataUrl={qr} />
        <Text style={styles.dataBlock}>{data}</Text>
      </View>
    </View>
  )
}

export function KeycardDocument({ data, qrUserKey, qrServicePubKey, qrPasscode }: Props) {
  return (
    <Document>
      {/* Page 1: Key material */}
      <Page size="A4" style={styles.page}>
        <View style={styles.header}>
          <Text style={styles.logo}>Vault</Text>
          <Text style={styles.chainLabel}>{data.chain}</Text>
          <Text style={styles.keycardLabel}>KeyCard</Text>
          <View style={styles.activationBox}>
            <Text style={styles.activationTitle}>Activation Code</Text>
            <Text style={styles.activationCode}>{data.activationCode}</Text>
          </View>
        </View>

        <View style={styles.warningBanner}>
          <Text>Print this document, or keep it securely offline. See below for FAQ.</Text>
        </View>

        <View style={styles.walletMeta}>
          <Text>Created on {data.createdAt} for wallet named:</Text>
          <Text style={{ fontFamily: 'Helvetica-Bold', fontSize: 14, marginTop: 4 }}>{data.walletName}</Text>
        </View>

        <Section
          label="A: User Key"
          description="Your MPC key share, encrypted with your wallet passphrase."
          data={data.encryptedUserKey}
          qr={qrUserKey}
        />
        <Section
          label="C: Service Public Key"
          description="The public key used to verify co-signed transactions."
          data={data.servicePublicKey}
          qr={qrServicePubKey}
        />
        <Section
          label="D: Encrypted Wallet Password"
          description="Your passphrase, encrypted by the service for account recovery purposes."
          data={data.encryptedPasscode}
          qr={qrPasscode}
        />
      </Page>

      {/* Page 2: FAQ */}
      <Page size="A4" style={styles.page}>
        <Text style={styles.faqTitle}>Vault KeyCard FAQ</Text>

        <Text style={styles.faqQ}>What is the KeyCard?</Text>
        <Text style={styles.faqA}>The KeyCard contains important information which can be used to recover your cryptocurrency wallet. Each Vault wallet has its own, unique KeyCard. If you have created multiple wallets, you should retain the KeyCard for each of them.</Text>

        <Text style={styles.faqQ}>What should I do with it?</Text>
        <Text style={styles.faqA}>Print the KeyCard and/or save the PDF to an offline storage device. Keep it in a safe place, such as a bank vault or home safe. Keep a second copy in a different location.</Text>

        <Text style={styles.faqQ}>What should I do if I lose it?</Text>
        <Text style={styles.faqA}>If you have lost all copies of your KeyCard, your funds are still safe, but this wallet should be considered at risk. Empty the wallet into a new wallet and discontinue use of the old wallet.</Text>

        <Text style={styles.faqQ}>What if someone sees my KeyCard?</Text>
        <Text style={styles.faqA}>All sensitive information on the KeyCard is encrypted with your passphrase or a service-held key. If your KeyCard is compromised, empty the wallet into a new one and discontinue use.</Text>

        <Text style={styles.faqQ}>What if I forget or lose my wallet password?</Text>
        <Text style={styles.faqA}>The service can use section D of your KeyCard to help recover access. Without the KeyCard, the service cannot recover funds from a wallet with a lost password.</Text>

        <Text style={styles.faqQ}>Should I write my wallet password on my KeyCard?</Text>
        <Text style={styles.faqA}>No. Security depends on there being no single point of attack. Keep your wallet password in a secure password manager such as 1Password or Bitwarden.</Text>
      </Page>
    </Document>
  )
}
```

- [ ] **Step 2: Create `front/src/lib/keycard/generateKeycardPdf.ts`**

```ts
import type { KeycardData } from './KeycardDocument'

async function tryQR(data: string): Promise<string | null> {
  try {
    const QRCode = (await import('qrcode')).default
    return await QRCode.toDataURL(data, { errorCorrectionLevel: 'L' })
  } catch {
    return null
  }
}

export async function generateKeycardPdf(data: KeycardData): Promise<void> {
  // Dynamic imports — @react-pdf/renderer must not run server-side
  const { pdf } = await import('@react-pdf/renderer')
  const { KeycardDocument } = await import('./KeycardDocument')
  const { createElement } = await import('react')

  const [qrUserKey, qrServicePubKey, qrPasscode] = await Promise.all([
    tryQR(data.encryptedUserKey),
    tryQR(data.servicePublicKey),
    tryQR(data.encryptedPasscode),
  ])

  const element = createElement(KeycardDocument, {
    data,
    qrUserKey,
    qrServicePubKey,
    qrPasscode,
  })

  const blob = await pdf(element).toBlob()
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `KeyCard-${data.walletName.replace(/\s+/g, '_')}-${data.chain}.pdf`
  link.click()
  URL.revokeObjectURL(url)
}

export type { KeycardData }
```

- [ ] **Step 3: TypeScript check**

```bash
cd /path/to/front && npx tsc --noEmit 2>&1 | grep -E "keycard|error" | head -20
```
Expected: no errors in the keycard files

- [ ] **Step 4: Commit**

```bash
cd /path/to/front
git add src/lib/keycard/KeycardDocument.tsx src/lib/keycard/generateKeycardPdf.ts
git commit -m "feat: add KeycardDocument PDF component and generateKeycardPdf utility"
```

---

## Task 9: Frontend — two-step `CreateWalletModal`

**Files:**
- Modify: `front/src/components/Modals/CreateWalletModal.tsx`
- Modify: `front/src/types/api.ts` (or wherever `CreateWalletRequest` is defined — add `confirm_passphrase`)

- [ ] **Step 1: Update API types**

Find `CreateWalletRequest` in `front/src/types/api.ts` (or similar). Add `confirm_passphrase`:
```ts
export interface CreateWalletRequest {
  chain: string
  label: string
  passphrase: string
  confirm_passphrase: string
}
```

Also add a type for the creation response:
```ts
export interface CreateWalletAdminResponse {
  wallet: Wallet
  encrypted_user_key: string
  service_public_key: string
  encrypted_passcode: string
  activation_code: string
}
```

Import `Wallet` from `@/types/wallet` if needed.

- [ ] **Step 2: Rewrite `CreateWalletModal.tsx`**

```tsx
import { useState } from 'react'
import { useRouter } from 'next/router'
import { Button, Input, Modal, useOverlayState } from '@heroui/react'
import { Eye, EyeOff } from 'lucide-react'
import { apiClient } from '@/lib/api/client'
import { useWallets } from '@/hooks/useWallets'
import { generateKeycardPdf } from '@/lib/keycard/generateKeycardPdf'
import type { CreateWalletAdminResponse } from '@/types/api'

const SUPPORTED_CHAINS = [
  { value: 'BTC', label: 'Bitcoin (BTC)' },
  { value: 'ETH', label: 'Ethereum (ETH)' },
  { value: 'MATIC', label: 'Polygon (MATIC)' },
]

interface KeycardState {
  walletId: string
  walletName: string
  chain: string
  encryptedUserKey: string
  servicePublicKey: string
  encryptedPasscode: string
  activationCode: string
}

export function CreateWalletModal() {
  const router = useRouter()
  const { mutate } = useWallets()
  const state = useOverlayState({ defaultOpen: true })

  // Step 1 state
  const [chain, setChain] = useState('ETH')
  const [label, setLabel] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [confirmPassphrase, setConfirmPassphrase] = useState('')
  const [showPass, setShowPass] = useState(false)
  const [showConfirm, setShowConfirm] = useState(false)
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(false)

  // Step 2 state
  const [step, setStep] = useState<'form' | 'keycard'>('form')
  const [keycardState, setKeycardState] = useState<KeycardState | null>(null)
  const [hasDownloaded, setHasDownloaded] = useState(false)
  const [activationCode, setActivationCode] = useState('')
  const [activating, setActivating] = useState(false)
  const [activateError, setActivateError] = useState('')

  const handleClose = () => {
    state.close()
    router.back()
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (passphrase.length < 12) {
      setError('Passphrase must be at least 12 characters')
      return
    }
    if (passphrase !== confirmPassphrase) {
      setError('Passphrases do not match')
      return
    }

    setIsLoading(true)
    try {
      const data = await apiClient.post('/v1/wallets', {
        chain,
        label,
        passphrase,
        confirm_passphrase: confirmPassphrase,
      }) as CreateWalletAdminResponse

      setKeycardState({
        walletId: data.wallet.id,
        walletName: label,
        chain,
        encryptedUserKey: data.encrypted_user_key,
        servicePublicKey: data.service_public_key,
        encryptedPasscode: data.encrypted_passcode,
        activationCode: data.activation_code,
      })
      setStep('keycard')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create wallet')
    } finally {
      setIsLoading(false)
    }
  }

  const handleDownload = async () => {
    if (!keycardState) return
    await generateKeycardPdf({
      walletName: keycardState.walletName,
      chain: keycardState.chain,
      createdAt: new Date().toDateString(),
      activationCode: keycardState.activationCode,
      encryptedUserKey: keycardState.encryptedUserKey,
      servicePublicKey: keycardState.servicePublicKey,
      encryptedPasscode: keycardState.encryptedPasscode,
    })
    setHasDownloaded(true)
  }

  const handleActivate = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!keycardState) return
    setActivateError('')
    setActivating(true)
    try {
      await apiClient.post(`/v1/wallets/${keycardState.walletId}/activate`, {
        code: activationCode,
      })
      await mutate()
      router.push(`/dashboard/wallets/${keycardState.walletId}`)
    } catch (err) {
      setActivateError(err instanceof Error ? err.message : 'Activation failed')
    } finally {
      setActivating(false)
    }
  }

  return (
    <Modal.Root state={state}>
      <Modal.Backdrop isDismissable onClick={handleClose} />
      <Modal.Container placement="center" size="md">
        <Modal.Dialog>
          {step === 'form' ? (
            <>
              <Modal.Header>
                <Modal.Heading>Create Wallet</Modal.Heading>
                <Modal.CloseTrigger onClick={handleClose} />
              </Modal.Header>
              <Modal.Body>
                <form onSubmit={handleSubmit} className="space-y-4">
                  <div>
                    <label className="block text-sm font-medium text-[var(--foreground)] mb-1">Chain</label>
                    <select
                      value={chain}
                      onChange={(e) => setChain(e.target.value)}
                      className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)]"
                    >
                      {SUPPORTED_CHAINS.map((c) => (
                        <option key={c.value} value={c.value}>{c.label}</option>
                      ))}
                    </select>
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-[var(--foreground)] mb-1">Wallet Name</label>
                    <Input
                      type="text"
                      value={label}
                      onChange={(e) => setLabel(e.target.value)}
                      placeholder="My BTC Wallet"
                      required
                      className="w-full"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-[var(--foreground)] mb-1">Passphrase</label>
                    <div className="relative">
                      <Input
                        type={showPass ? 'text' : 'password'}
                        value={passphrase}
                        onChange={(e) => setPassphrase(e.target.value)}
                        placeholder="Min. 12 characters"
                        required
                        className="w-full pr-10"
                      />
                      <button
                        type="button"
                        onClick={() => setShowPass((v) => !v)}
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-[var(--muted)]"
                      >
                        {showPass ? <EyeOff size={16} /> : <Eye size={16} />}
                      </button>
                    </div>
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-[var(--foreground)] mb-1">Confirm Passphrase</label>
                    <div className="relative">
                      <Input
                        type={showConfirm ? 'text' : 'password'}
                        value={confirmPassphrase}
                        onChange={(e) => setConfirmPassphrase(e.target.value)}
                        placeholder="Repeat passphrase"
                        required
                        className="w-full pr-10"
                      />
                      <button
                        type="button"
                        onClick={() => setShowConfirm((v) => !v)}
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-[var(--muted)]"
                      >
                        {showConfirm ? <EyeOff size={16} /> : <Eye size={16} />}
                      </button>
                    </div>
                  </div>

                  {error && (
                    <p className="text-sm text-red-500 bg-red-500/10 rounded px-3 py-2">{error}</p>
                  )}

                  <div className="flex gap-3 pt-2">
                    <Button type="button" variant="ghost" onClick={handleClose} className="flex-1">Cancel</Button>
                    <Button type="submit" variant="primary" isDisabled={isLoading} className="flex-1">
                      {isLoading ? 'Creating…' : 'Create Wallet'}
                    </Button>
                  </div>
                </form>
              </Modal.Body>
            </>
          ) : (
            <>
              <Modal.Header>
                <Modal.Heading>Save Your KeyCard</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-4">
                  <p className="text-sm text-[var(--foreground)]">
                    Your wallet has been created. Download your KeyCard PDF and store it safely
                    before continuing. You will need it to recover your wallet.
                  </p>

                  <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
                    <strong>Activation Code: {keycardState?.activationCode}</strong>
                    <br />
                    This code is printed on your KeyCard. Enter it below to confirm you have saved it.
                  </div>

                  <Button
                    variant="primary"
                    onClick={handleDownload}
                    className="w-full"
                  >
                    Download KeyCard PDF
                  </Button>

                  <form onSubmit={handleActivate} className="space-y-3">
                    <div>
                      <label className="block text-sm font-medium text-[var(--foreground)] mb-1">
                        Enter Activation Code from PDF
                      </label>
                      <Input
                        type="text"
                        inputMode="numeric"
                        maxLength={6}
                        value={activationCode}
                        onChange={(e) => setActivationCode(e.target.value.replace(/\D/g, ''))}
                        placeholder="6-digit code"
                        disabled={!hasDownloaded}
                        className="w-full"
                      />
                    </div>

                    {activateError && (
                      <p className="text-sm text-red-500 bg-red-500/10 rounded px-3 py-2">{activateError}</p>
                    )}

                    <Button
                      type="submit"
                      variant="primary"
                      isDisabled={!hasDownloaded || activationCode.length !== 6 || activating}
                      className="w-full"
                    >
                      {activating ? 'Activating…' : 'Activate Wallet'}
                    </Button>
                  </form>
                </div>
              </Modal.Body>
            </>
          )}
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Root>
  )
}
```

- [ ] **Step 3: TypeScript check**

```bash
cd /path/to/front && npx tsc --noEmit 2>&1 | grep -v "node_modules" | head -30
```
Expected: no errors in `CreateWalletModal.tsx`

- [ ] **Step 4: Start LocalStack + backend, smoke test end-to-end**

```bash
# Terminal 1: start LocalStack
docker compose up localstack -d

# Terminal 2: start backend
cd /path/to/back && go run main.go

# Terminal 3: start frontend
cd /path/to/front && npm run dev
```

Manual test:
1. Log in to dashboard
2. Navigate to Wallets
3. Click "Create Wallet"
4. Fill: Chain=ETH, Name=Test, passphrase=test-passphrase-long, confirm same
5. Submit → should show KeyCard step with activation code
6. Click "Download KeyCard PDF" → PDF should download
7. Enter the 6-digit code → click Activate
8. Should navigate to `/dashboard/wallets/{id}`
9. Wallet should appear in the list with `status: active`

- [ ] **Step 5: Commit**

```bash
cd /path/to/front
git add src/components/Modals/CreateWalletModal.tsx src/types/api.ts
git commit -m "feat: two-step CreateWalletModal with KeyCard PDF download and activation"
```

---

## Notes for the implementer

### Go imports reference

Files that will need `crypto/rand` imported by name to avoid ambiguity with `math/rand`:
```go
import (
    cryptorand "crypto/rand"
    "math/big"
)
// Usage: cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
```
`service.go` currently uses `"github.com/redis/go-redis/v9"` for `redis.Client` — the package import `rand` won't conflict since redis doesn't export `rand`.

### Goravel ORM `.Exec()` API

If `facades.Orm().Query().Exec(sql)` is not available (check Goravel v1.17 ORM docs), use:
```go
facades.Orm().Query().Raw(sql).Scan(nil)
```
as an alternative for DDL statements.

### `smithy-go` availability

Before Task 5 Step 1, verify the module is present:
```bash
cd /path/to/back && go list -m github.com/aws/smithy-go
```
If absent: `go get github.com/aws/smithy-go` — it is typically an indirect dep of `aws-sdk-go-v2`.

### `@react-pdf/renderer` + Next.js pages router

If TypeScript complains about `@react-pdf/renderer` types, add `"@react-pdf/renderer"` to `compilerOptions.types` or suppress with `// @ts-expect-error` on the import line. The dynamic import in `generateKeycardPdf.ts` avoids SSR issues completely.
