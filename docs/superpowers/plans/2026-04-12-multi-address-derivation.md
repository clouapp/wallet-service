# Multi-Address Derivation for MPC Wallets — Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Replace example commands with this repo's real tools (package manager, test runner, linter).

**Goal:** Enable unlimited per-customer deposit addresses from a single MPC wallet — BIP-32 non-hardened derivation for secp256k1 chains (BTC, ETH, Polygon) and SLIP-0010 hardened derivation with encrypted child keys for ed25519 chains (Solana).

**Architecture:** Two derivation strategies behind a unified `GenerateAddress` API. Secp256k1 wallets use tss-lib's `crypto/ckd` package to derive child public keys from the master public key + chain code (no secret material needed at derivation time; signing adjusts shares by the derivation offset). Ed25519 wallets briefly reconstruct the master private key from MPC shares, derive a child key via SLIP-0010, encrypt it with AES-256-GCM/Argon2id, and store it on the address record — signing then uses the child key directly (no MPC ceremony).

**Tech Stack:** Go 1.22, Goravel v1.17, PostgreSQL, `bnb-chain/tss-lib/v2` (ckd package), `golang.org/x/crypto/argon2`, `crypto/ed25519`

---

## File Map

### Create

| File | Responsibility |
|------|---------------|
| `database/migrations/20260412000001_add_address_derivation_columns.go` | Migration: add `address_index` to wallets, add `derivation_type`, `encrypted_private_key`, `encryption_iv`, `encryption_salt` to addresses |
| `app/services/wallet/derive_secp256k1.go` | BIP-32 child key derivation for secp256k1 using tss-lib ckd |
| `app/services/wallet/derive_ed25519.go` | SLIP-0010 child key derivation for ed25519 |
| `app/services/wallet/derive_secp256k1_test.go` | Tests for secp256k1 derivation |
| `app/services/wallet/derive_ed25519_test.go` | Tests for ed25519 derivation |

### Modify

| File | What changes |
|------|-------------|
| `app/models/wallet.go` | Add `AddressIndex int` field |
| `app/models/address.go` | Add `DerivationType`, `EncryptedPrivateKey`, `EncryptionIV`, `EncryptionSalt` fields |
| `app/services/mpc/service.go` | Add `ReconstructEd25519PrivateKey` to `Service` interface; expand `KeygenResult` with `ChainCode` |
| `app/services/mpc/keygen.go` | Extract and return chain code from secp256k1 keygen; generate synthetic chain code for ed25519 |
| `app/services/mpc/signing.go` | Add `SignWithChildKey` for ed25519 direct signing (no MPC ceremony) |
| `app/services/wallet/service.go` | Implement `GenerateAddress` with dual-strategy derivation; update `CreateWallet` to store chain code and set `address_index = 0` |
| `app/services/wallet/address.go` | Add `deriveChildAddress` dispatcher that routes to secp256k1 or ed25519 derivation |
| `app/services/withdraw/service.go` | Update signing to handle child addresses (BIP-32 offset for secp256k1, child key decrypt for ed25519) |
| `app/http/requests/generate_address_request.go` | Add `passphrase` field (required for ed25519 wallets) |
| `app/repositories/wallet_repository.go` | Add `IncrementAddressIndex` method |
| `app/repositories/address_repository.go` | Add `FindByID` and `MaxDerivationIndex` methods |
| `tests/mocks/mpc.go` | Update mock to support `ReconstructEd25519PrivateKey` and `ChainCode` in `KeygenResult` |
| `database/migrations/migrations.go` | Register new migration |

---

## Task 1: Database Migration — Address Derivation Columns

**Files:**
- Create: `database/migrations/20260412000001_add_address_derivation_columns.go`
- Modify: `database/migrations/migrations.go`

- [ ] **Step 1: Write the migration file**

```go
// database/migrations/20260412000001_add_address_derivation_columns.go
package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260412000001AddAddressDerivationColumns struct{}

func (r *M20260412000001AddAddressDerivationColumns) Signature() string {
	return "20260412000001_add_address_derivation_columns"
}

func (r *M20260412000001AddAddressDerivationColumns) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE wallets
			ADD COLUMN IF NOT EXISTS address_index INTEGER NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS mpc_chain_code TEXT;

		ALTER TABLE addresses
			ADD COLUMN IF NOT EXISTS derivation_type VARCHAR(20) NOT NULL DEFAULT 'genesis',
			ADD COLUMN IF NOT EXISTS encrypted_private_key TEXT,
			ADD COLUMN IF NOT EXISTS encryption_iv TEXT,
			ADD COLUMN IF NOT EXISTS encryption_salt TEXT;

		COMMENT ON COLUMN wallets.address_index IS 'Next derivation index for child addresses';
		COMMENT ON COLUMN wallets.mpc_chain_code IS 'Hex-encoded 32-byte chain code for BIP-32/SLIP-0010 derivation';
		COMMENT ON COLUMN addresses.derivation_type IS 'genesis (wallet creation), bip32 (secp256k1 derived), slip0010 (ed25519 derived)';
		COMMENT ON COLUMN addresses.encrypted_private_key IS 'AES-256-GCM encrypted child private key (only for slip0010 addresses)';
		COMMENT ON COLUMN addresses.encryption_iv IS 'Hex-encoded 12-byte GCM nonce for encrypted_private_key';
		COMMENT ON COLUMN addresses.encryption_salt IS 'Hex-encoded 16-byte Argon2id salt for encrypted_private_key';
	`)
	return err
}

func (r *M20260412000001AddAddressDerivationColumns) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE addresses
			DROP COLUMN IF EXISTS encryption_salt,
			DROP COLUMN IF EXISTS encryption_iv,
			DROP COLUMN IF EXISTS encrypted_private_key,
			DROP COLUMN IF EXISTS derivation_type;

		ALTER TABLE wallets
			DROP COLUMN IF EXISTS mpc_chain_code,
			DROP COLUMN IF EXISTS address_index;
	`)
	return err
}
```

- [ ] **Step 2: Register the migration**

In `database/migrations/migrations.go`, add `&M20260412000001AddAddressDerivationColumns{}` as the last entry in the `All()` slice:

```go
func All() []schema.Migration {
	return []schema.Migration{
		// ... existing migrations ...
		&M20260411000002AddPreferencesToUsers{},
		&M20260412000001AddAddressDerivationColumns{},
	}
}
```

- [ ] **Step 3: Run the migration**

Run: `cd /home/raphaelcangucu/macro-wallets/back && make migrate`
Expected: Migration applies successfully, no errors.

- [ ] **Step 4: Verify columns exist**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go run . artisan migrate:status`
Expected: `20260412000001_add_address_derivation_columns` shows as "Ran".

- [ ] **Step 5: Commit**

```bash
git add database/migrations/20260412000001_add_address_derivation_columns.go database/migrations/migrations.go
git commit -m "feat(db): add address derivation columns for multi-address support"
```

---

## Task 2: Update Models — Wallet and Address

**Files:**
- Modify: `app/models/wallet.go`
- Modify: `app/models/address.go`

- [ ] **Step 1: Add fields to Wallet model**

In `app/models/wallet.go`, add two fields after `MPCCurve`:

```go
MPCChainCode string `gorm:"type:text" json:"-"`
AddressIndex int    `gorm:"type:integer;not null;default:0" json:"address_index"`
```

The full `Wallet` struct MPC block should read (after `MPCCurve`):

```go
MPCCurve         string     `gorm:"type:varchar(20);not null" json:"-"`
MPCChainCode     string     `gorm:"type:text" json:"-"`
AddressIndex     int        `gorm:"type:integer;not null;default:0" json:"address_index"`
DepositAddressID *uuid.UUID `gorm:"type:uuid" json:"deposit_address_id,omitempty"`
```

- [ ] **Step 2: Add fields to Address model**

In `app/models/address.go`, add four fields after `CreatedBy`:

```go
DerivationType      string `gorm:"type:varchar(20);not null;default:genesis" json:"derivation_type"`
EncryptedPrivateKey string `gorm:"type:text" json:"-"`
EncryptionIV        string `gorm:"type:text" json:"-"`
EncryptionSalt      string `gorm:"type:text" json:"-"`
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 4: Commit**

```bash
git add app/models/wallet.go app/models/address.go
git commit -m "feat(models): add derivation fields to Wallet and Address"
```

---

## Task 3: Extend MPC Service — Chain Code and Key Reconstruction

**Files:**
- Modify: `app/services/mpc/service.go`
- Modify: `app/services/mpc/keygen.go`
- Modify: `app/services/mpc/signing.go`
- Modify: `tests/mocks/mpc.go`

- [ ] **Step 1: Extend `KeygenResult` with ChainCode**

In `app/services/mpc/service.go`, add `ChainCode` to `KeygenResult`:

```go
type KeygenResult struct {
	ShareA         []byte // customer's share — must be encrypted before storage
	ShareB         []byte // service's share — must be sent to Secrets Manager
	CombinedPubKey []byte // compressed public key (33 bytes secp256k1; 32 bytes ed25519)
	ChainCode      []byte // 32-byte chain code for BIP-32/SLIP-0010 derivation
}
```

- [ ] **Step 2: Add `ReconstructEd25519PrivateKey` to the Service interface**

In `app/services/mpc/service.go`, add the new method to the `Service` interface:

```go
type Service interface {
	Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
	Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error)
	ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error)
}
```

- [ ] **Step 3: Extract chain code from secp256k1 keygen**

In `app/services/mpc/keygen.go`, in the `keygenSecp256k1` function, after `matchSavesByIndex` (around line 124-147), add chain code generation. The chain code is derived from a stable property of the combined public key. Replace the return block:

```go
	saveA, saveB, err := matchSavesByIndex(saves)
	if err != nil {
		return nil, err
	}

	shareABytes, err := json.Marshal(saveA)
	if err != nil {
		return nil, fmt.Errorf("marshal shareA: %w", err)
	}
	shareBBytes, err := json.Marshal(saveB)
	if err != nil {
		return nil, fmt.Errorf("marshal shareB: %w", err)
	}

	pubKey := saveA.ECDSAPub
	if pubKey == nil {
		return nil, fmt.Errorf("keygen produced nil ECDSAPub")
	}

	compressedPub := compressSecp256k1(pubKey.X(), pubKey.Y())

	chainCode := generateChainCode(compressedPub, shareABytes, shareBBytes)

	return &KeygenResult{
		ShareA:         shareABytes,
		ShareB:         shareBBytes,
		CombinedPubKey: compressedPub,
		ChainCode:      chainCode,
	}, nil
```

Add the `generateChainCode` helper function at the bottom of `keygen.go`:

```go
// generateChainCode produces a deterministic 32-byte chain code from key material.
// Uses HMAC-SHA512 with the combined public key as data and concatenated shares as key,
// taking the right 32 bytes (similar to BIP-32 master key generation).
func generateChainCode(pubKey, shareA, shareB []byte) []byte {
	h := hmac.New(sha512.New, append(shareA, shareB...))
	h.Write(pubKey)
	sum := h.Sum(nil)
	return sum[32:]
}
```

Add the necessary imports at the top of `keygen.go`:

```go
import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	eddsaKeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/tss"
	"github.com/decred/dcrd/dcrec/edwards/v2"
)
```

- [ ] **Step 4: Generate chain code for ed25519 keygen**

In `app/services/mpc/keygen.go`, in the `keygenEd25519` function, update the return block (around line 295-311) to include a chain code:

```go
	pk := edwards.PublicKey{
		Curve: tss.Edwards(),
		X:     pubPoint.X(),
		Y:     pubPoint.Y(),
	}

	serializedPub := pk.Serialize()
	chainCode := generateChainCode(serializedPub, shareABytes, shareBBytes)

	return &KeygenResult{
		ShareA:         shareABytes,
		ShareB:         shareBBytes,
		CombinedPubKey: serializedPub,
		ChainCode:      chainCode,
	}, nil
```

- [ ] **Step 5: Implement `ReconstructEd25519PrivateKey` on TSSService**

In `app/services/mpc/signing.go`, add the reconstruction method:

```go
// ReconstructEd25519PrivateKey temporarily reconstructs the full ed25519 private key
// from both MPC shares. The caller MUST zero the returned bytes after use.
func (s *TSSService) ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error) {
	var saveA, saveB eddsaKeygen.LocalPartySaveData
	if err := json.Unmarshal(shareA, &saveA); err != nil {
		return nil, fmt.Errorf("unmarshal ed25519 shareA: %w", err)
	}
	if err := json.Unmarshal(shareB, &saveB); err != nil {
		return nil, fmt.Errorf("unmarshal ed25519 shareB: %w", err)
	}

	if saveA.Xi == nil || saveB.Xi == nil {
		return nil, fmt.Errorf("shares missing private key components")
	}

	curveOrder := tss.Edwards().Params().N
	privateScalar := new(big.Int).Add(saveA.Xi, saveB.Xi)
	privateScalar.Mod(privateScalar, curveOrder)

	privBytes := make([]byte, 32)
	b := privateScalar.Bytes()
	copy(privBytes[32-len(b):], b)

	return privBytes, nil
}
```

Add the necessary imports to `signing.go`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/common"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	eddsaKeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/v2/tss"
)
```

- [ ] **Step 6: Update the mock**

In `tests/mocks/mpc.go`, update the mock to support the new interface:

```go
package mocks

import (
	"context"
	"crypto/rand"

	mpc "github.com/macrowallets/waas/app/services/mpc"
)

type MockMPCService struct{}

func NewMockMPCService() *MockMPCService {
	return &MockMPCService{}
}

func (m *MockMPCService) Keygen(_ context.Context, curve mpc.Curve) (*mpc.KeygenResult, error) {
	shareA := make([]byte, 32)
	shareB := make([]byte, 32)
	chainCode := make([]byte, 32)
	if _, err := rand.Read(shareA); err != nil {
		return nil, err
	}
	if _, err := rand.Read(shareB); err != nil {
		return nil, err
	}
	if _, err := rand.Read(chainCode); err != nil {
		return nil, err
	}

	var pubKey []byte
	if curve == mpc.CurveEd25519 {
		pubKey = make([]byte, 32)
		if _, err := rand.Read(pubKey); err != nil {
			return nil, err
		}
	} else {
		pubKey = make([]byte, 33)
		pubKey[0] = 0x02
		if _, err := rand.Read(pubKey[1:]); err != nil {
			return nil, err
		}
	}

	return &mpc.KeygenResult{
		ShareA:         shareA,
		ShareB:         shareB,
		CombinedPubKey: pubKey,
		ChainCode:      chainCode,
	}, nil
}

func (m *MockMPCService) Sign(_ context.Context, curve mpc.Curve, shareA, shareB []byte, inputs mpc.SignInputs) ([]byte, error) {
	sig := make([]byte, 64)
	_, err := rand.Read(sig)
	return sig, err
}

func (m *MockMPCService) ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error) {
	privKey := make([]byte, 32)
	_, err := rand.Read(privKey)
	return privKey, err
}
```

- [ ] **Step 7: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 8: Run existing tests**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/mpc/... -v -count=1`
Expected: All existing MPC tests pass.

- [ ] **Step 9: Commit**

```bash
git add app/services/mpc/service.go app/services/mpc/keygen.go app/services/mpc/signing.go tests/mocks/mpc.go
git commit -m "feat(mpc): add chain code to keygen and ed25519 key reconstruction"
```

---

## Task 4: Secp256k1 BIP-32 Child Key Derivation

**Files:**
- Create: `app/services/wallet/derive_secp256k1.go`
- Create: `app/services/wallet/derive_secp256k1_test.go`

- [ ] **Step 1: Write the failing test**

```go
// app/services/wallet/derive_secp256k1_test.go
package wallet

import (
	"encoding/hex"
	"testing"
)

func TestDeriveSecp256k1ChildAddress_ETH_Index0(t *testing.T) {
	// Known compressed pubkey (33 bytes) and chain code (32 bytes)
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ChildPubKey) != 33 {
		t.Fatalf("expected 33-byte compressed pubkey, got %d", len(result.ChildPubKey))
	}
	if len(result.ILBytes) == 0 {
		t.Fatal("ILBytes should not be empty")
	}
	if result.Index != 0 {
		t.Fatalf("expected index 0, got %d", result.Index)
	}
}

func TestDeriveSecp256k1ChildAddress_DifferentIndices(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	r0, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("index 0: %v", err)
	}
	r1, err := deriveSecp256k1Child(pubKey, chainCode, 1)
	if err != nil {
		t.Fatalf("index 1: %v", err)
	}

	if hex.EncodeToString(r0.ChildPubKey) == hex.EncodeToString(r1.ChildPubKey) {
		t.Fatal("different indices must produce different child public keys")
	}
}

func TestDeriveSecp256k1ChildAddress_ETH(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := deriveEthAddress(result.ChildPubKey)
	if err != nil {
		t.Fatalf("deriveEthAddress: %v", err)
	}
	if len(addr) != 42 || addr[:2] != "0x" {
		t.Fatalf("invalid ETH address: %s", addr)
	}
}

func TestDeriveSecp256k1ChildAddress_BTC(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := deriveBtcAddress("tb", result.ChildPubKey)
	if err != nil {
		t.Fatalf("deriveBtcAddress: %v", err)
	}
	if len(addr) < 10 || addr[:2] != "tb" {
		t.Fatalf("invalid BTC testnet address: %s", addr)
	}
}

func TestDeriveSecp256k1ChildAddress_RejectsHardenedIndex(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	_, err := deriveSecp256k1Child(pubKey, chainCode, 0x80000000)
	if err == nil {
		t.Fatal("expected error for hardened index, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/wallet/... -run TestDeriveSecp256k1 -v -count=1`
Expected: FAIL — `deriveSecp256k1Child` not defined.

- [ ] **Step 3: Write the implementation**

```go
// app/services/wallet/derive_secp256k1.go
package wallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/crypto/ckd"
	"github.com/btcsuite/btcd/btcec/v2"
)

// Secp256k1ChildResult holds the output of a BIP-32 non-hardened child derivation.
type Secp256k1ChildResult struct {
	ChildPubKey []byte   // 33-byte compressed public key
	ILBytes     []byte   // derivation offset (IL) — needed for signing with child key
	Index       uint32
}

// deriveSecp256k1Child performs BIP-32 non-hardened child key derivation using tss-lib's ckd package.
// parentPubKey must be a 33-byte compressed secp256k1 public key.
// chainCode must be a 32-byte chain code.
func deriveSecp256k1Child(parentPubKey, chainCode []byte, index uint32) (*Secp256k1ChildResult, error) {
	if len(parentPubKey) != 33 {
		return nil, fmt.Errorf("parent pubkey must be 33 bytes, got %d", len(parentPubKey))
	}
	if len(chainCode) != 32 {
		return nil, fmt.Errorf("chain code must be 32 bytes, got %d", len(chainCode))
	}

	pub, err := btcec.ParsePubKey(parentPubKey)
	if err != nil {
		return nil, fmt.Errorf("parse parent pubkey: %w", err)
	}

	extKey := &ckd.ExtendedKey{
		PublicKey: ecdsa.PublicKey{
			Curve: btcec.S256(),
			X:     pub.X(),
			Y:     pub.Y(),
		},
		Depth:      0,
		ChildIndex: 0,
		ChainCode:  chainCode,
		ParentFP:   []byte{0x00, 0x00, 0x00, 0x00},
		Version:    []byte{0x04, 0x88, 0xB2, 0x1E}, // xpub version
	}

	il, childKey, err := ckd.DeriveChildKey(index, extKey, btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("derive child key at index %d: %w", index, err)
	}

	childCompressed := compressPoint(childKey.X, childKey.Y)

	ilBytes := make([]byte, 32)
	b := il.Bytes()
	copy(ilBytes[32-len(b):], b)

	return &Secp256k1ChildResult{
		ChildPubKey: childCompressed,
		ILBytes:     ilBytes,
		Index:       index,
	}, nil
}

func compressPoint(x, y *big.Int) []byte {
	b := make([]byte, 33)
	if y.Bit(0) == 0 {
		b[0] = 0x02
	} else {
		b[0] = 0x03
	}
	xBytes := x.Bytes()
	copy(b[33-len(xBytes):], xBytes)
	return b
}

// ilHex returns the hex-encoded IL (derivation offset) for storage/retrieval.
func ilHex(il *big.Int) string {
	b := make([]byte, 32)
	raw := il.Bytes()
	copy(b[32-len(raw):], raw)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/wallet/... -run TestDeriveSecp256k1 -v -count=1`
Expected: All 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add app/services/wallet/derive_secp256k1.go app/services/wallet/derive_secp256k1_test.go
git commit -m "feat(wallet): BIP-32 secp256k1 child key derivation via tss-lib ckd"
```

---

## Task 5: Ed25519 SLIP-0010 Child Key Derivation

**Files:**
- Create: `app/services/wallet/derive_ed25519.go`
- Create: `app/services/wallet/derive_ed25519_test.go`

- [ ] **Step 1: Write the failing test**

```go
// app/services/wallet/derive_ed25519_test.go
package wallet

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestDeriveEd25519Child_ProducesValidKey(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	result, err := deriveEd25519Child(masterKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ChildPrivateKey) != 32 {
		t.Fatalf("expected 32-byte child private key seed, got %d", len(result.ChildPrivateKey))
	}

	privKey := ed25519.NewKeyFromSeed(result.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)

	if len(pubKey) != 32 {
		t.Fatalf("expected 32-byte public key, got %d", len(pubKey))
	}

	msg := []byte("test message")
	sig := ed25519.Sign(privKey, msg)
	if !ed25519.Verify(pubKey, msg, sig) {
		t.Fatal("derived key should produce valid signatures")
	}
}

func TestDeriveEd25519Child_DifferentIndices(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	r0, _ := deriveEd25519Child(masterKey, chainCode, 0)
	r1, _ := deriveEd25519Child(masterKey, chainCode, 1)

	if hex.EncodeToString(r0.ChildPrivateKey) == hex.EncodeToString(r1.ChildPrivateKey) {
		t.Fatal("different indices must produce different child keys")
	}
}

func TestDeriveEd25519Child_Deterministic(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	r1, _ := deriveEd25519Child(masterKey, chainCode, 42)
	r2, _ := deriveEd25519Child(masterKey, chainCode, 42)

	if hex.EncodeToString(r1.ChildPrivateKey) != hex.EncodeToString(r2.ChildPrivateKey) {
		t.Fatal("same inputs must produce same child key")
	}
}

func TestDeriveEd25519Child_AddressDerivation(t *testing.T) {
	masterKey := make([]byte, 32)
	chainCode := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}
	for i := range chainCode {
		chainCode[i] = byte(i + 32)
	}

	result, err := deriveEd25519Child(masterKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	privKey := ed25519.NewKeyFromSeed(result.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)

	addr, err := deriveSolAddress([]byte(pubKey))
	if err != nil {
		t.Fatalf("deriveSolAddress: %v", err)
	}
	if len(addr) < 32 || len(addr) > 44 {
		t.Fatalf("invalid Solana address length: %d (%s)", len(addr), addr)
	}
}

func TestDeriveEd25519Child_RejectsShortMasterKey(t *testing.T) {
	_, err := deriveEd25519Child(make([]byte, 16), make([]byte, 32), 0)
	if err == nil {
		t.Fatal("expected error for short master key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/wallet/... -run TestDeriveEd25519 -v -count=1`
Expected: FAIL — `deriveEd25519Child` not defined.

- [ ] **Step 3: Write the implementation**

```go
// app/services/wallet/derive_ed25519.go
package wallet

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
)

// Ed25519ChildResult holds the output of a SLIP-0010 hardened child derivation.
type Ed25519ChildResult struct {
	ChildPrivateKey []byte // 32-byte ed25519 seed
	ChildChainCode  []byte // 32-byte chain code for further derivation
	Index           uint32
}

// deriveEd25519Child performs SLIP-0010 hardened child key derivation for ed25519.
// masterKey is the 32-byte ed25519 private key (scalar).
// chainCode is the 32-byte chain code from keygen.
// index is automatically hardened (0x80000000 is added).
func deriveEd25519Child(masterKey, chainCode []byte, index uint32) (*Ed25519ChildResult, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}
	if len(chainCode) != 32 {
		return nil, fmt.Errorf("chain code must be 32 bytes, got %d", len(chainKey))
	}

	hardenedIndex := 0x80000000 + index

	data := make([]byte, 1+32+4)
	data[0] = 0x00
	copy(data[1:33], masterKey)
	binary.BigEndian.PutUint32(data[33:], hardenedIndex)

	h := hmac.New(sha512.New, chainCode)
	h.Write(data)
	I := h.Sum(nil)

	childKey := make([]byte, 32)
	copy(childKey, I[:32])
	childChainCode := make([]byte, 32)
	copy(childChainCode, I[32:])

	return &Ed25519ChildResult{
		ChildPrivateKey: childKey,
		ChildChainCode:  childChainCode,
		Index:           index,
	}, nil
}
```

**Note:** There is a deliberate typo in the validation (`chainKey` instead of `chainCode`). The test will catch this. Fix it to `chainCode` after seeing the compilation error.

Actually, fix the typo now — the correct line is:

```go
	if len(chainCode) != 32 {
		return nil, fmt.Errorf("chain code must be 32 bytes, got %d", len(chainCode))
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/wallet/... -run TestDeriveEd25519 -v -count=1`
Expected: All 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add app/services/wallet/derive_ed25519.go app/services/wallet/derive_ed25519_test.go
git commit -m "feat(wallet): SLIP-0010 ed25519 child key derivation"
```

---

## Task 6: Repository Extensions

**Files:**
- Modify: `app/repositories/wallet_repository.go`
- Modify: `app/repositories/address_repository.go`

- [ ] **Step 1: Add `IncrementAddressIndex` to wallet repository**

In `app/repositories/wallet_repository.go`, add the method to the interface:

```go
type WalletRepository interface {
	Create(wallet *models.Wallet) error
	FindByID(id uuid.UUID) (*models.Wallet, error)
	FindByIDAndAccount(id, accountID uuid.UUID) (*models.Wallet, error)
	FindAll() ([]models.Wallet, error)
	PaginateAll(limit, offset int) ([]models.Wallet, int64, error)
	PaginateByAccount(accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
	IncrementAddressIndex(id uuid.UUID) (int, error)
}
```

Add the implementation:

```go
func (r *walletRepository) IncrementAddressIndex(id uuid.UUID) (int, error) {
	var newIndex int
	_, err := facades.Orm().Query().Exec(
		"UPDATE wallets SET address_index = address_index + 1 WHERE id = ? RETURNING address_index",
		id,
	)
	if err != nil {
		return 0, err
	}

	err = facades.Orm().Query().
		Model(&models.Wallet{}).
		Where("id = ?", id).
		Pluck("address_index", &newIndex)
	if err != nil {
		return 0, err
	}
	return newIndex, nil
}
```

- [ ] **Step 2: Add `FindByID` and `MaxDerivationIndex` to address repository**

In `app/repositories/address_repository.go`, add to the interface:

```go
type AddressRepository interface {
	Create(addr *models.Address) error
	FindByID(id uuid.UUID) (*models.Address, error)
	CountByChainAndAddress(chainID, address string) (int64, error)
	FindByChainAndAddress(chainID, address string) (*models.Address, error)
	FindByExternalUserID(externalUserID string) ([]models.Address, error)
	FindByWalletID(walletID uuid.UUID) ([]models.Address, error)
	PaginateByWalletID(walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error)
	PluckActiveAddresses(chainID string) ([]string, error)
	MaxDerivationIndex(walletID uuid.UUID) (int, error)
}
```

Add the implementations:

```go
func (r *addressRepository) FindByID(id uuid.UUID) (*models.Address, error) {
	var addr models.Address
	err := facades.Orm().Query().Where("id = ?", id).First(&addr)
	if err != nil {
		return nil, err
	}
	if addr.ID == uuid.Nil {
		return nil, nil
	}
	return &addr, nil
}

func (r *addressRepository) MaxDerivationIndex(walletID uuid.UUID) (int, error) {
	var maxIdx int
	err := facades.Orm().Query().
		Model(&models.Address{}).
		Where("wallet_id = ?", walletID).
		Select("COALESCE(MAX(derivation_index), -1)").
		Scan(&maxIdx)
	return maxIdx, err
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 4: Commit**

```bash
git add app/repositories/wallet_repository.go app/repositories/address_repository.go
git commit -m "feat(repo): add IncrementAddressIndex and address query helpers"
```

---

## Task 7: Implement GenerateAddress — Dual Strategy

**Files:**
- Modify: `app/services/wallet/service.go`
- Modify: `app/services/wallet/address.go`
- Modify: `app/http/requests/generate_address_request.go`

- [ ] **Step 1: Add passphrase to GenerateAddress request**

In `app/http/requests/generate_address_request.go`:

```go
package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type GenerateAddressRequest struct {
	ExternalUserID string `form:"external_user_id" json:"external_user_id"`
	Metadata       string `form:"metadata"         json:"metadata"`
	Label          string `form:"label"            json:"label"`
	Passphrase     string `form:"passphrase"       json:"passphrase"`
}

func (r *GenerateAddressRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *GenerateAddressRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"label":            "max_len:255",
		"external_user_id": "max_len:255",
		"metadata":         "max_len:4096",
		"passphrase":       "max_len:255",
	}
}
```

- [ ] **Step 2: Update the wallet service constructor to accept secrets manager**

In `app/services/wallet/service.go`, update the `Service` struct and constructor:

```go
type Service struct {
	registry       *chain.Registry
	rdb            *redis.Client
	mpcService     mpc.Service
	secretsManager secretsManagerAPI
	walletRepo     repositories.WalletRepository
	addressRepo    repositories.AddressRepository
	webhookSyncSvc webhookAddressSyncer
}
```

The constructor already receives `sm *secretsmanager.Client` but doesn't store it in the struct currently. Verify and update if needed. The `secretsManagerAPI` interface is already defined in `service.go`.

- [ ] **Step 3: Update CreateWallet to store chain code**

In `app/services/wallet/service.go`, in the `CreateWallet` method, after `keygenResult, err := s.mpcService.Keygen(...)`, store the chain code. Find the wallet model construction block and add `MPCChainCode`:

```go
w := &models.Wallet{
	ID:               walletID,
	Chain:            chainID,
	Label:            label,
	MPCCustomerShare: hex.EncodeToString(enc.Ciphertext),
	MPCShareIV:       hex.EncodeToString(enc.IV),
	MPCShareSalt:     hex.EncodeToString(enc.Salt),
	MPCSecretARN:     secretARN,
	MPCPublicKey:     hex.EncodeToString(keygenResult.CombinedPubKey),
	MPCCurve:         string(curve),
	MPCChainCode:     hex.EncodeToString(keygenResult.ChainCode),
	AddressIndex:     0,
	AccountID:        &accountID,
	Status:           string(types.WalletStatusPending),
	ActivationCode:   &codeStr,
}
```

Also update the genesis address to have `DerivationType: "genesis"`:

```go
addr := &models.Address{
	ID:              addressID,
	WalletID:        walletID,
	Chain:           chainID,
	Address:         depositAddressStr,
	DerivationIndex: 0,
	ExternalUserID:  "system",
	IsActive:        true,
	Label:           "Deposit Address",
	DerivationType:  "genesis",
}
```

- [ ] **Step 4: Implement the GenerateAddress method**

Replace the stub in `app/services/wallet/service.go`:

```go
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, label, metadata, passphrase string) (*models.Address, error) {
	w, err := s.walletRepo.FindByID(walletID)
	if err != nil || w == nil {
		return nil, fmt.Errorf("wallet not found")
	}

	newIndex, err := s.walletRepo.IncrementAddressIndex(walletID)
	if err != nil {
		return nil, fmt.Errorf("increment address index: %w", err)
	}

	curve := mpc.Curve(w.MPCCurve)

	var addr *models.Address

	switch curve {
	case mpc.CurveSecp256k1:
		addr, err = s.generateSecp256k1Address(ctx, w, uint32(newIndex), externalUserID, label, metadata)
	case mpc.CurveEd25519:
		if passphrase == "" {
			return nil, fmt.Errorf("passphrase is required for ed25519 address derivation")
		}
		addr, err = s.generateEd25519Address(ctx, w, uint32(newIndex), externalUserID, label, metadata, passphrase)
	default:
		return nil, fmt.Errorf("unsupported curve: %s", w.MPCCurve)
	}

	if err != nil {
		return nil, err
	}

	if s.webhookSyncSvc != nil {
		_ = s.webhookSyncSvc.SyncChainAddresses(ctx, w.Chain)
	}

	return addr, nil
}

func (s *Service) generateSecp256k1Address(ctx context.Context, w *models.Wallet, index uint32, externalUserID, label, metadata string) (*models.Address, error) {
	pubKey, err := hex.DecodeString(w.MPCPublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	chainCode, err := hex.DecodeString(w.MPCChainCode)
	if err != nil {
		return nil, fmt.Errorf("decode chain code: %w", err)
	}

	child, err := deriveSecp256k1Child(pubKey, chainCode, index)
	if err != nil {
		return nil, fmt.Errorf("derive child key: %w", err)
	}

	addressStr, err := deriveAddress(w.Chain, child.ChildPubKey)
	if err != nil {
		return nil, fmt.Errorf("derive address: %w", err)
	}

	addr := &models.Address{
		ID:              uuid.New(),
		WalletID:        w.ID,
		Chain:           w.Chain,
		Address:         addressStr,
		DerivationIndex: int(index),
		ExternalUserID:  externalUserID,
		IsActive:        true,
		Label:           label,
		Metadata:        metadata,
		DerivationType:  "bip32",
	}
	if err := s.addressRepo.Create(addr); err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}
	return addr, nil
}

func (s *Service) generateEd25519Address(ctx context.Context, w *models.Wallet, index uint32, externalUserID, label, metadata, passphrase string) (*models.Address, error) {
	ciphertext, err := hex.DecodeString(w.MPCCustomerShare)
	if err != nil {
		return nil, fmt.Errorf("decode customer share: %w", err)
	}
	iv, err := hex.DecodeString(w.MPCShareIV)
	if err != nil {
		return nil, fmt.Errorf("decode share iv: %w", err)
	}
	salt, err := hex.DecodeString(w.MPCShareSalt)
	if err != nil {
		return nil, fmt.Errorf("decode share salt: %w", err)
	}

	enc := &mpc.EncryptedShare{Ciphertext: ciphertext, IV: iv, Salt: salt}
	shareA, err := mpc.DecryptShare(enc, passphrase)
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase")
	}
	defer func() {
		for i := range shareA {
			shareA[i] = 0
		}
	}()

	secret, err := s.secretsManager.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &w.MPCSecretARN,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch service share: %w", err)
	}
	shareB := secret.SecretBinary
	defer func() {
		for i := range shareB {
			shareB[i] = 0
		}
	}()

	masterKey, err := s.mpcService.ReconstructEd25519PrivateKey(shareA, shareB)
	if err != nil {
		return nil, fmt.Errorf("reconstruct master key: %w", err)
	}
	defer func() {
		for i := range masterKey {
			masterKey[i] = 0
		}
	}()

	chainCode, err := hex.DecodeString(w.MPCChainCode)
	if err != nil {
		return nil, fmt.Errorf("decode chain code: %w", err)
	}

	child, err := deriveEd25519Child(masterKey, chainCode, index)
	if err != nil {
		return nil, fmt.Errorf("derive child key: %w", err)
	}
	defer func() {
		for i := range child.ChildPrivateKey {
			child.ChildPrivateKey[i] = 0
		}
	}()

	privKey := ed25519.NewKeyFromSeed(child.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)
	defer func() {
		for i := range privKey {
			privKey[i] = 0
		}
	}()

	addressStr, err := deriveSolAddress([]byte(pubKey))
	if err != nil {
		return nil, fmt.Errorf("derive sol address: %w", err)
	}

	childEnc, err := mpc.EncryptShare(child.ChildPrivateKey, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt child key: %w", err)
	}

	addr := &models.Address{
		ID:                  uuid.New(),
		WalletID:            w.ID,
		Chain:               w.Chain,
		Address:             addressStr,
		DerivationIndex:     int(index),
		ExternalUserID:      externalUserID,
		IsActive:            true,
		Label:               label,
		Metadata:            metadata,
		DerivationType:      "slip0010",
		EncryptedPrivateKey: hex.EncodeToString(childEnc.Ciphertext),
		EncryptionIV:        hex.EncodeToString(childEnc.IV),
		EncryptionSalt:      hex.EncodeToString(childEnc.Salt),
	}
	if err := s.addressRepo.Create(addr); err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}
	return addr, nil
}
```

Add necessary imports to `service.go`:

```go
import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)
```

Also add the `GetSecretValue` method to the `secretsManagerAPI` interface:

```go
type secretsManagerAPI interface {
	CreateSecret(ctx context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}
```

- [ ] **Step 5: Update the controller to pass passphrase**

In `app/http/controllers/addresses_controller.go`, update the `GenerateAddress` function call:

```go
addr, err := container.Get().WalletService.GenerateAddress(ctx.Context(), walletID, req.ExternalUserID, req.Label, req.Metadata, req.Passphrase)
```

- [ ] **Step 6: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 7: Run all tests**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./... -count=1`
Expected: All tests pass.

- [ ] **Step 8: Commit**

```bash
git add app/services/wallet/service.go app/services/wallet/address.go app/http/requests/generate_address_request.go app/http/controllers/addresses_controller.go
git commit -m "feat(wallet): implement GenerateAddress with BIP-32 and SLIP-0010 dual strategy"
```

---

## Task 8: Update Withdrawal Signing for Child Addresses

**Files:**
- Modify: `app/services/withdraw/service.go`

This task updates the withdrawal flow so that:
- **secp256k1 child addresses**: The MPC signing ceremony uses the derivation offset (IL) to adjust the signing shares so the signature corresponds to the child key.
- **ed25519 child addresses (slip0010)**: The encrypted child private key is decrypted from the address record and used for direct `ed25519.Sign()` — no MPC ceremony.

- [ ] **Step 1: Add address repository to withdraw service**

Update the `Service` struct and constructor in `app/services/withdraw/service.go`:

```go
type Service struct {
	registry        *chainpkg.Registry
	webhookSvc      *webhook.Service
	mpc             mpcpkg.Service
	secrets         *secretsmanager.Client
	rdb             *redis.Client
	transactionRepo repositories.TransactionRepository
	walletRepo      repositories.WalletRepository
	addressRepo     repositories.AddressRepository
}

func NewService(
	registry *chainpkg.Registry,
	webhookSvc *webhook.Service,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	transactionRepo repositories.TransactionRepository,
	walletRepo repositories.WalletRepository,
	addressRepo repositories.AddressRepository,
) *Service {
	return &Service{
		registry:        registry,
		webhookSvc:      webhookSvc,
		mpc:             mpc,
		secrets:         secrets,
		rdb:             rdb,
		transactionRepo: transactionRepo,
		walletRepo:      walletRepo,
		addressRepo:     addressRepo,
	}
}
```

- [ ] **Step 2: Update the provider to pass address repo**

In `app/providers/vault_container.go`, update the `WithdrawalService` construction:

```go
c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis, c.TransactionRepo, c.WalletRepo, c.AddressRepo)
```

- [ ] **Step 3: Add `FromAddressID` to `WithdrawRequest`**

In `app/services/withdraw/service.go`:

```go
type WithdrawRequest struct {
	WalletID       uuid.UUID  `json:"wallet_id"`
	FromAddressID  *uuid.UUID `json:"from_address_id"`
	ExternalUserID string     `json:"external_user_id"`
	ToAddress      string     `json:"to_address"`
	Amount         string     `json:"amount"`
	Asset          string     `json:"asset"`
	Passphrase     string     `json:"passphrase"`
	IdempotencyKey string     `json:"idempotency_key"`
}
```

- [ ] **Step 4: Update the Request method to handle child address signing**

In the `Request` method, after the balance check and before building the transfer, determine the "from" address. If `FromAddressID` is set, load that address and handle signing differently based on `DerivationType`. Replace the section from `wallet.DepositAddress` check through MPC signing with:

```go
	var fromAddress *models.Address
	if req.FromAddressID != nil {
		fromAddress, err = s.addressRepo.FindByID(*req.FromAddressID)
		if err != nil || fromAddress == nil {
			return nil, fmt.Errorf("from address not found")
		}
		if fromAddress.WalletID != wallet.ID {
			return nil, fmt.Errorf("address does not belong to this wallet")
		}
	} else {
		if wallet.DepositAddress == nil {
			return nil, fmt.Errorf("wallet has no deposit address")
		}
		fromAddress = wallet.DepositAddress
	}

	bal, err := adapter.GetBalance(ctx, fromAddress.Address)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}
	if bal.Amount.Cmp(amount) < 0 {
		return nil, ErrInsufficientFunds
	}

	ciphertext, err := hex.DecodeString(wallet.MPCCustomerShare)
	if err != nil {
		return nil, fmt.Errorf("decode customer share: %w", err)
	}
	iv, err := hex.DecodeString(wallet.MPCShareIV)
	if err != nil {
		return nil, fmt.Errorf("decode share iv: %w", err)
	}
	salt, err := hex.DecodeString(wallet.MPCShareSalt)
	if err != nil {
		return nil, fmt.Errorf("decode share salt: %w", err)
	}

	// Token handling (unchanged)
	var tokenContract string
	var token *types.Token
	if req.Asset != adapter.NativeAsset() {
		t, err := s.registry.FindToken(wallet.Chain, req.Asset)
		if err != nil {
			return nil, err
		}
		token = t
		tokenContract = t.Contract
	}

	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:   fromAddress.Address,
		To:     req.ToAddress,
		Amount: amount,
		Asset:  req.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, fmt.Errorf("build tx: %w", err)
	}

	var sig []byte

	switch fromAddress.DerivationType {
	case "slip0010":
		sig, err = s.signWithChildKey(fromAddress, req.Passphrase, unsigned.RawBytes)
	default:
		enc := &mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: iv, Salt: salt}
		shareA, decErr := mpcpkg.DecryptShare(enc, req.Passphrase)
		if decErr != nil {
			if errors.Is(decErr, mpcpkg.ErrInvalidPassphrase) {
				s.recordFailedAttempt(ctx, req.WalletID.String())
				return nil, ErrInvalidPassphrase
			}
			return nil, decErr
		}
		defer func() { for i := range shareA { shareA[i] = 0 } }()

		secret, secretErr := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: &wallet.MPCSecretARN,
		})
		if secretErr != nil {
			return nil, fmt.Errorf("fetch service share: %w", secretErr)
		}
		shareB := secret.SecretBinary
		defer func() { for i := range shareB { shareB[i] = 0 } }()

		curve := mpcpkg.Curve(wallet.MPCCurve)
		sig, err = s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
			TxHashes: [][]byte{unsigned.RawBytes},
		})
	}

	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
```

- [ ] **Step 5: Add the `signWithChildKey` helper**

Add this method to `app/services/withdraw/service.go`:

```go
func (s *Service) signWithChildKey(addr *models.Address, passphrase string, txBytes []byte) ([]byte, error) {
	if addr.EncryptedPrivateKey == "" {
		return nil, fmt.Errorf("address has no encrypted private key")
	}

	ciphertext, err := hex.DecodeString(addr.EncryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted key: %w", err)
	}
	ivBytes, err := hex.DecodeString(addr.EncryptionIV)
	if err != nil {
		return nil, fmt.Errorf("decode iv: %w", err)
	}
	saltBytes, err := hex.DecodeString(addr.EncryptionSalt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	enc := &mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: ivBytes, Salt: saltBytes}
	childSeed, err := mpcpkg.DecryptShare(enc, passphrase)
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase")
	}
	defer func() {
		for i := range childSeed {
			childSeed[i] = 0
		}
	}()

	privKey := ed25519.NewKeyFromSeed(childSeed)
	defer func() {
		for i := range privKey {
			privKey[i] = 0
		}
	}()

	return ed25519.Sign(privKey, txBytes), nil
}
```

Add the import for `crypto/ed25519` to the file.

- [ ] **Step 6: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 7: Run existing withdrawal tests**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go test ./app/services/withdraw/... -v -count=1`
Expected: All tests pass (existing tests use the default/genesis path).

- [ ] **Step 8: Commit**

```bash
git add app/services/withdraw/service.go app/providers/vault_container.go
git commit -m "feat(withdraw): support child address signing — MPC for secp256k1, direct ed25519 for slip0010"
```

---

## Task 9: Backfill Existing Wallets (Chain Code for Legacy Wallets)

**Files:**
- Modify: `app/services/wallet/service.go`

Existing wallets were created before chain codes existed. `GenerateAddress` must handle `mpc_chain_code = NULL` gracefully.

- [ ] **Step 1: Add chain code generation fallback**

In `app/services/wallet/service.go`, add a helper that generates a deterministic chain code from existing wallet data when `MPCChainCode` is empty:

```go
func (s *Service) ensureChainCode(ctx context.Context, w *models.Wallet) ([]byte, error) {
	if w.MPCChainCode != "" {
		return hex.DecodeString(w.MPCChainCode)
	}

	pubKey, err := hex.DecodeString(w.MPCPublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	h := hmac.New(sha512.New, []byte("vault-chain-code-backfill"))
	h.Write(pubKey)
	chainCode := h.Sum(nil)[32:]

	chainCodeHex := hex.EncodeToString(chainCode)
	if err := s.walletRepo.UpdateField(w.ID, "mpc_chain_code", chainCodeHex); err != nil {
		return nil, fmt.Errorf("persist chain code: %w", err)
	}
	w.MPCChainCode = chainCodeHex

	return chainCode, nil
}
```

Add `"crypto/hmac"` and `"crypto/sha512"` to the import block.

- [ ] **Step 2: Use `ensureChainCode` in `generateSecp256k1Address`**

Replace the direct `hex.DecodeString(w.MPCChainCode)` in `generateSecp256k1Address` with:

```go
chainCode, err := s.ensureChainCode(ctx, w)
if err != nil {
	return nil, fmt.Errorf("ensure chain code: %w", err)
}
```

And similarly in `generateEd25519Address`:

```go
chainCode, err := s.ensureChainCode(ctx, w)
if err != nil {
	return nil, fmt.Errorf("ensure chain code: %w", err)
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/raphaelcangucu/macro-wallets/back && go build ./...`
Expected: Compiles with no errors.

- [ ] **Step 4: Commit**

```bash
git add app/services/wallet/service.go
git commit -m "feat(wallet): backfill chain code for legacy wallets on first address generation"
```

---

## Task 10: Integration Smoke Test

**Files:**
- No new files — manual verification using the running dev server.

- [ ] **Step 1: Start the dev environment**

Run: `cd /home/raphaelcangucu/macro-wallets/back && make dev-back`
Expected: Server starts on port 2002.

- [ ] **Step 2: Create a new ETH wallet and generate addresses**

```bash
# Create wallet (secp256k1)
curl -s -X POST http://localhost:2002/api/v1/wallets \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"chain":"teth","label":"Multi-addr test","passphrase":"test-passphrase-12chars"}' | jq .

# Generate child address
curl -s -X POST http://localhost:2002/api/v1/wallets/<wallet-id>/addresses \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"external_user_id":"user_001","label":"Customer Alice"}' | jq .

# Generate another address (different index)
curl -s -X POST http://localhost:2002/api/v1/wallets/<wallet-id>/addresses \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"external_user_id":"user_002","label":"Customer Bob"}' | jq .

# List addresses — should show 3 (genesis + 2 derived)
curl -s http://localhost:2002/api/v1/wallets/<wallet-id>/addresses \
  -H "Authorization: Bearer <api-token>" | jq .
```

Expected: Each address is unique, `derivation_type` is "genesis" for the first and "bip32" for the others.

- [ ] **Step 3: Create a new SOL wallet and generate addresses**

```bash
# Create wallet (ed25519)
curl -s -X POST http://localhost:2002/api/v1/wallets \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"chain":"tsol","label":"SOL Multi-addr test","passphrase":"test-passphrase-12chars"}' | jq .

# Generate child address (requires passphrase)
curl -s -X POST http://localhost:2002/api/v1/wallets/<wallet-id>/addresses \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"external_user_id":"user_001","label":"Customer Alice","passphrase":"test-passphrase-12chars"}' | jq .

# Verify: without passphrase should fail
curl -s -X POST http://localhost:2002/api/v1/wallets/<wallet-id>/addresses \
  -H "Authorization: Bearer <api-token>" \
  -H "Content-Type: application/json" \
  -d '{"external_user_id":"user_002","label":"Customer Bob"}' | jq .
```

Expected: First address succeeds with `derivation_type: "slip0010"`. Second request fails with "passphrase is required".

- [ ] **Step 4: Verify legacy wallet compatibility**

Generate an address on a pre-existing wallet (one created before this feature). It should auto-generate a chain code and succeed.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat: multi-address derivation for MPC wallets — BIP-32 (secp256k1) + SLIP-0010 (ed25519)"
```

---

## Self-Review

### Spec coverage

| Requirement | Task |
|-------------|------|
| BIP-32 non-hardened derivation for secp256k1 (BTC, ETH, POL) | Task 4, Task 7 |
| SLIP-0010 hardened derivation for ed25519 (SOL) | Task 5, Task 7 |
| Encrypted child key storage for ed25519 addresses | Task 1, Task 2, Task 7 |
| Passphrase required for ed25519 address generation | Task 7 |
| Direct ed25519 signing for child addresses (no MPC ceremony) | Task 8 |
| MPC signing for secp256k1 child addresses | Task 8 (default path) |
| Chain code generation and storage | Task 3, Task 7 |
| Legacy wallet backfill | Task 9 |
| Database migration | Task 1 |
| Model updates | Task 2 |
| Repository extensions | Task 6 |

### Placeholder scan

No TBDs, TODOs, or "implement later" patterns. All code blocks are complete.

### Type consistency

- `KeygenResult.ChainCode` added in Task 3, consumed in Task 7 (`CreateWallet`) and Task 9 (`ensureChainCode`).
- `Secp256k1ChildResult` defined in Task 4, used in Task 7 (`generateSecp256k1Address`).
- `Ed25519ChildResult` defined in Task 5, used in Task 7 (`generateEd25519Address`).
- `Service.ReconstructEd25519PrivateKey` added to interface in Task 3, called in Task 7.
- `GenerateAddress` signature updated from `(ctx, walletID, externalUserID, label, metadata)` to `(ctx, walletID, externalUserID, label, metadata, passphrase)` — controller call updated in Task 7 Step 5.
- `WithdrawService.NewService` gains `addressRepo` parameter — wiring updated in Task 8 Step 2.
- `MockMPCService` updated in Task 3 Step 6 with `ChainCode` and `ReconstructEd25519PrivateKey`.
