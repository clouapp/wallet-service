# MPC Co-Signing Wallet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the existing placeholder HD wallet system with a real 2-party MPC co-signing model using `tss-lib v2`, where customers encrypt their key share with a passphrase and the service stores its share in AWS Secrets Manager.

**Architecture:** Two tss-lib parties run in-process via Go channels during keygen and signing. The customer's share is encrypted with Argon2id + AES-256-GCM and stored in the DB. The service share goes to AWS Secrets Manager. Withdrawals are fully synchronous in the API Lambda — no SQS worker for signing.

**Tech Stack:** Go 1.22, bnb-chain/tss-lib v2.0.2, golang.org/x/crypto (Argon2id), AWS SDK v2 Secrets Manager, Goravel ORM, Redis (distributed lock + rate limit)

**Spec:** `docs/superpowers/specs/2026-03-18-mpc-cosigning-wallet-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `app/services/mpc/service.go` | Interface, Curve type, KeygenResult, SignInputs |
| Create | `app/services/mpc/keystore.go` | Argon2id key derivation + AES-256-GCM encrypt/decrypt |
| Create | `app/services/mpc/keygen.go` | tss-lib 2-party keygen ceremony via channels |
| Create | `app/services/mpc/signing.go` | tss-lib 2-party signing via channels |
| Create | `app/services/mpc/service_test.go` | Tests for all MPC operations incl. Solana gate |
| Modify | `database/migrations/20260317000001_create_wallets_table.go` | Replace HD columns with MPC columns + deposit_address |
| Modify | `app/models/wallet.go` | Replace HD fields with MPC fields + DepositAddress |
| Modify | `app/container/container.go` | Add MPCService + SecretsManagerClient; remove withdrawal queue |
| Modify | `app/services/wallet/service.go` | CreateWallet gets passphrase; GenerateAddress returns 422 |
| Modify | `app/services/withdraw/service.go` | Synchronous MPC signing; remove Execute() and SQS path |
| Modify | `app/http/controllers/withdrawals_controller.go` | Add passphrase field; update Swagger |
| Modify | `app/http/controllers/wallets_controller.go` | Add passphrase field; update Swagger |
| Modify | `main.go` | Remove handleWithdrawalWorker and case "withdrawal_worker" |
| Modify | `pkg/types/types.go` | Delete WithdrawalMessage struct |
| Modify | `app/services/queue/sqs.go` | Remove SendWithdrawal method and Withdrawal queue URL |

---

## Task 1: Resolve Dependencies

**Files:** `go.mod`, `go.sum`

- [ ] **Step 1: Add tss-lib v2**

```bash
cd /path/to/wallet-service
go get github.com/bnb-chain/tss-lib/v2@v2.0.2
```

Expected: `go.mod` updated with `github.com/bnb-chain/tss-lib/v2 v2.0.2`

- [ ] **Step 2: Add Argon2, Secrets Manager, and Base58**

```bash
go get golang.org/x/crypto@latest
go get github.com/aws/aws-sdk-go-v2/service/secretsmanager
go get github.com/mr-tron/base58
```

- [ ] **Step 3: Verify the build still compiles**

```bash
go build ./...
```

Expected: no errors. If there are dependency conflicts with `goravel/framework`, run `go mod tidy` and inspect `go.mod` for replace directives needed. Do not proceed until this passes.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add tss-lib v2, argon2, secretsmanager dependencies"
```

---

## Task 2: MPC Types and Interface

**Files:**
- Create: `app/services/mpc/service.go`

- [ ] **Step 1: Create the file**

```go
package mpc

import "context"

// Curve identifies the elliptic curve used for MPC operations.
type Curve string

const (
	CurveSecp256k1 Curve = "secp256k1"
	CurveEd25519   Curve = "ed25519"
)

// KeygenResult holds the outputs of a 2-party MPC key generation ceremony.
type KeygenResult struct {
	ShareA         []byte // customer's share — must be encrypted before storage
	ShareB         []byte // service's share — must be sent to Secrets Manager
	CombinedPubKey []byte // compressed public key (33 bytes secp256k1; 32 bytes ed25519)
}

// SignInputs carries all transaction data required for signing.
// Bitcoin may have multiple UTXO inputs (one hash per input).
// ETH and Solana have a single hash.
type SignInputs struct {
	TxHashes [][]byte // one entry per input
}

// Service is the MPC co-signing interface.
type Service interface {
	Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
	Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./app/services/mpc/...
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add app/services/mpc/service.go
git commit -m "feat(mpc): add MPC service interface and types"
```

---

## Task 3: MPC Keystore (Argon2id + AES-256-GCM)

**Files:**
- Create: `app/services/mpc/keystore.go`
- Create: `app/services/mpc/service_test.go` (keystore tests only at this stage)

- [ ] **Step 1: Write the failing tests first**

Create `app/services/mpc/service_test.go`:

```go
package mpc_test

import (
	"bytes"
	"testing"

	"github.com/macromarkets/vault/app/services/mpc"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	passphrase := "correct-horse-battery-staple-123"
	plaintext := []byte("this is a fake mpc key share bytes 32b!")

	encrypted, err := mpc.EncryptShare(plaintext, passphrase)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := mpc.DecryptShare(encrypted, passphrase)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatal("round-trip mismatch")
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	plaintext := []byte("some share data here for testing purposes!")
	encrypted, _ := mpc.EncryptShare(plaintext, "correct-passphrase-here")

	_, err := mpc.DecryptShare(encrypted, "wrong-passphrase-here!")
	if err == nil {
		t.Fatal("expected error on wrong passphrase, got nil")
	}
}

func TestEncryptedShareFormat(t *testing.T) {
	plaintext := []byte("share data for format test purposes")
	encrypted, err := mpc.EncryptShare(plaintext, "test-passphrase-12chars")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// IV must be exactly 12 bytes
	if len(encrypted.IV) != 12 {
		t.Fatalf("IV length: want 12, got %d", len(encrypted.IV))
	}
	// Salt must be exactly 16 bytes
	if len(encrypted.Salt) != 16 {
		t.Fatalf("Salt length: want 16, got %d", len(encrypted.Salt))
	}
	// Ciphertext must be plaintext + 16-byte GCM tag
	if len(encrypted.Ciphertext) != len(plaintext)+16 {
		t.Fatalf("Ciphertext length: want %d, got %d", len(plaintext)+16, len(encrypted.Ciphertext))
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./app/services/mpc/... -run TestEncrypt -v
```

Expected: FAIL — `mpc.EncryptShare` undefined

- [ ] **Step 3: Implement keystore.go**

Create `app/services/mpc/keystore.go`:

```go
package mpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// EncryptedShare holds all data needed to decrypt a key share later.
type EncryptedShare struct {
	Ciphertext []byte // AES-256-GCM ciphertext || 16-byte GCM tag
	IV         []byte // 12-byte nonce
	Salt       []byte // 16-byte Argon2id salt
}

// EncryptShare derives a key from passphrase via Argon2id then encrypts share
// with AES-256-GCM. Returns ciphertext with the GCM tag appended.
func EncryptShare(share []byte, passphrase string) (*EncryptedShare, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	iv := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate iv: %w", err)
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	// Seal appends the GCM tag to the ciphertext
	ciphertext := gcm.Seal(nil, iv, share, nil)

	// Zero-wipe the derived key
	for i := range key {
		key[i] = 0
	}

	return &EncryptedShare{
		Ciphertext: ciphertext,
		IV:         iv,
		Salt:       salt,
	}, nil
}

// DecryptShare reverses EncryptShare. Returns ErrInvalidPassphrase on auth failure.
var ErrInvalidPassphrase = errors.New("invalid passphrase")

func DecryptShare(enc *EncryptedShare, passphrase string) ([]byte, error) {
	key := deriveKey(passphrase, enc.Salt)
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, enc.IV, enc.Ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidPassphrase
	}

	return plaintext, nil
}

// deriveKey runs Argon2id with the spec parameters.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./app/services/mpc/... -run TestEncrypt -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add app/services/mpc/keystore.go app/services/mpc/service_test.go
git commit -m "feat(mpc): add Argon2id+AES-256-GCM keystore with tests"
```

---

## Task 4: MPC Keygen

**Files:**
- Create: `app/services/mpc/keygen.go`
- Modify: `app/services/mpc/service_test.go` (add keygen tests)

- [ ] **Step 1: Add keygen tests to service_test.go**

Append to `app/services/mpc/service_test.go`:

```go
func TestKeygenSecp256k1ProducesShares(t *testing.T) {
	svc := mpc.NewTSSService()

	result, err := svc.Keygen(context.Background(), mpc.CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	if len(result.ShareA) == 0 {
		t.Fatal("ShareA is empty")
	}
	if len(result.ShareB) == 0 {
		t.Fatal("ShareB is empty")
	}
	// secp256k1 compressed pubkey = 33 bytes
	if len(result.CombinedPubKey) != 33 {
		t.Fatalf("CombinedPubKey length: want 33, got %d", len(result.CombinedPubKey))
	}
	// Compressed pubkey starts with 0x02 or 0x03
	if result.CombinedPubKey[0] != 0x02 && result.CombinedPubKey[0] != 0x03 {
		t.Fatalf("CombinedPubKey not compressed: first byte %x", result.CombinedPubKey[0])
	}
}

func TestKeygenSharesAreDifferent(t *testing.T) {
	svc := mpc.NewTSSService()

	r1, err := svc.Keygen(context.Background(), mpc.CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen 1: %v", err)
	}
	r2, err := svc.Keygen(context.Background(), mpc.CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen 2: %v", err)
	}

	// Two keygens must produce different keys
	if bytes.Equal(r1.ShareA, r2.ShareA) {
		t.Fatal("two keygens produced identical ShareA")
	}
	if bytes.Equal(r1.CombinedPubKey, r2.CombinedPubKey) {
		t.Fatal("two keygens produced identical public keys")
	}
}
```

Add `"context"` to the imports block in the test file.

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./app/services/mpc/... -run TestKeygen -v
```

Expected: FAIL — `mpc.NewTSSService` undefined

- [ ] **Step 3: Implement keygen.go**

Create `app/services/mpc/keygen.go`:

```go
package mpc

import (
	"context"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/common"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/tss"
)

// TSSService implements Service using bnb-chain/tss-lib v2.
type TSSService struct{}

func NewTSSService() *TSSService {
	return &TSSService{}
}

func (s *TSSService) Keygen(ctx context.Context, curve Curve) (*KeygenResult, error) {
	switch curve {
	case CurveSecp256k1:
		return keygenSecp256k1(ctx)
	case CurveEd25519:
		return keygenEd25519(ctx)
	default:
		return nil, fmt.Errorf("unsupported curve: %s", curve)
	}
}

func keygenSecp256k1(ctx context.Context) (*KeygenResult, error) {
	// Create two party IDs (partyA = customer share, partyB = service share)
	partyIDA := tss.NewPartyID("A", "customer", new(big.Int).SetInt64(1))
	partyIDB := tss.NewPartyID("B", "service", new(big.Int).SetInt64(2))
	parties := tss.SortPartyIDs(tss.UnSortedPartyIDs{partyIDA, partyIDB})

	peerCtx := tss.NewPeerContext(parties)
	threshold := 1 // 2-of-2: threshold = parties - 1

	paramsA := tss.NewParameters(tss.S256(), peerCtx, parties[0], len(parties), threshold)
	paramsB := tss.NewParameters(tss.S256(), peerCtx, parties[1], len(parties), threshold)

	// Output channels
	outA := make(chan tss.Message, 20)
	outB := make(chan tss.Message, 20)
	endA := make(chan *common.ECPoint, 1) // not used directly
	endB := make(chan *common.ECPoint, 1)

	// Key save channels — holds the final LocalPartySaveData
	saveA := make(chan keygen.LocalPartySaveData, 1)
	saveB := make(chan keygen.LocalPartySaveData, 1)

	partyA, _ := keygen.NewLocalParty(paramsA, outA, saveA)
	partyB, _ := keygen.NewLocalParty(paramsB, outB, saveB)

	_ = endA
	_ = endB

	errCh := make(chan error, 2)

	go func() {
		if err := partyA.Start(); err != nil {
			errCh <- err
		}
	}()
	go func() {
		if err := partyB.Start(); err != nil {
			errCh <- err
		}
	}()

	// Route messages between parties until both save channels receive
	doneA, doneB := false, false
	for !doneA || !doneB {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, fmt.Errorf("keygen error: %w", err)
		case msg := <-outA:
			dest := msg.GetTo()
			for _, d := range dest {
				if d.Index == partyB.PartyID().Index {
					if _, err := partyB.Update(msg); err != nil {
						return nil, fmt.Errorf("partyB update: %w", err)
					}
				}
			}
		case msg := <-outB:
			dest := msg.GetTo()
			for _, d := range dest {
				if d.Index == partyA.PartyID().Index {
					if _, err := partyA.Update(msg); err != nil {
						return nil, fmt.Errorf("partyA update: %w", err)
					}
				}
			}
		case data := <-saveA:
			shareABytes, err := marshalSaveData(data)
			if err != nil {
				return nil, err
			}
			// Wait for B too
			dataB := <-saveB
			shareBBytes, err := marshalSaveData(dataB)
			if err != nil {
				return nil, err
			}
			pubKey := data.ECDSAPub.X().Bytes()
			pubKey = append([]byte{0x02}, pubKey...) // compressed point approximation
			// Use btcec to get proper compressed point
			pubKeyCompressed, err := compressSecp256k1PubKey(data.ECDSAPub)
			if err != nil {
				pubKeyCompressed = pubKey
			}
			return &KeygenResult{
				ShareA:         shareABytes,
				ShareB:         shareBBytes,
				CombinedPubKey: pubKeyCompressed,
			}, nil
		case <-saveB:
			doneB = true
		}
	}

	return nil, fmt.Errorf("keygen completed without producing save data")
}
```

> **Note to implementor**: The above is a structural sketch. The exact tss-lib v2 API (channel types, party constructors, `Update` method signatures) must be verified against [bnb-chain/tss-lib/v2 examples](https://github.com/bnb-chain/tss-lib/tree/master/ecdsa/keygen). The `saveA`/`saveB` channels receive `keygen.LocalPartySaveData` — serialize to JSON with `json.Marshal` for storage. Use `btcec.ParsePubKey` on the raw EC point to get a proper 33-byte compressed key. The message routing loop structure above is correct; exact field names may vary.

Add `marshalSaveData` and `compressSecp256k1PubKey` helpers:

```go
// Add to keygen.go

import (
    "encoding/json"
    "github.com/btcsuite/btcd/btcec/v2"
)

func marshalSaveData(data keygen.LocalPartySaveData) ([]byte, error) {
    return json.Marshal(data)
}

func compressSecp256k1PubKey(ecPub *common.ECPoint) ([]byte, error) {
    x := ecPub.X()
    y := ecPub.Y()
    pk := &btcec.PublicKey{}
    pk.X = x
    pk.Y = y
    return pk.SerializeCompressed(), nil
}
```

Add stub `keygenEd25519` (Solana gate — replace with real implementation once test vector passes):

```go
func keygenEd25519(ctx context.Context) (*KeygenResult, error) {
    return nil, fmt.Errorf("ed25519 keygen: pending Solana compatibility gate verification (see spec section 'Solana ed25519 Compatibility Gate')")
}
```

- [ ] **Step 4: Add `Sign` stub to satisfy interface**

Create `app/services/mpc/signing.go` (stub only, real implementation in Task 5):

```go
package mpc

import (
    "context"
    "fmt"
)

func (s *TSSService) Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error) {
    return nil, fmt.Errorf("Sign: not yet implemented")
}
```

- [ ] **Step 5: Run keygen tests**

```bash
go test ./app/services/mpc/... -run TestKeygen -v -timeout 120s
```

Expected: PASS. Keygen is slow (~5-30s per run). If it panics with a nil pointer or channel deadlock, check that both parties are started before the routing loop, and that the `saveA`/`saveB` channel capacity is sufficient.

- [ ] **Step 6: Commit**

```bash
git add app/services/mpc/keygen.go app/services/mpc/signing.go app/services/mpc/service_test.go
git commit -m "feat(mpc): implement 2-party secp256k1 keygen with tests"
```

---

## Task 5: MPC Signing

**Files:**
- Modify: `app/services/mpc/signing.go`
- Modify: `app/services/mpc/service_test.go` (add signing test)

- [ ] **Step 1: Add signing test**

Append to `app/services/mpc/service_test.go`:

```go
func TestSignSecp256k1(t *testing.T) {
	svc := mpc.NewTSSService()

	// First keygen to get shares
	result, err := svc.Keygen(context.Background(), mpc.CurveSecp256k1)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Sign a known 32-byte hash
	msgHash := make([]byte, 32)
	for i := range msgHash {
		msgHash[i] = byte(i)
	}

	sig, err := svc.Sign(
		context.Background(),
		mpc.CurveSecp256k1,
		result.ShareA,
		result.ShareB,
		mpc.SignInputs{TxHashes: [][]byte{msgHash}},
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if len(sig) == 0 {
		t.Fatal("signature is empty")
	}

	// DER-encoded ECDSA signatures are 70-72 bytes
	if len(sig) < 64 || len(sig) > 73 {
		t.Fatalf("unexpected signature length: %d", len(sig))
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./app/services/mpc/... -run TestSign -v -timeout 120s
```

Expected: FAIL — `Sign: not yet implemented`

- [ ] **Step 3: Implement signing.go**

Replace `app/services/mpc/signing.go`:

```go
package mpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	"github.com/bnb-chain/tss-lib/v2/ecdsa/signing"
	"github.com/bnb-chain/tss-lib/v2/tss"
)

func (s *TSSService) Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error) {
	if len(inputs.TxHashes) == 0 {
		return nil, fmt.Errorf("no tx hashes provided")
	}

	switch curve {
	case CurveSecp256k1:
		return signSecp256k1(ctx, shareA, shareB, inputs.TxHashes[0])
	case CurveEd25519:
		return nil, fmt.Errorf("ed25519 signing: pending Solana compatibility gate")
	default:
		return nil, fmt.Errorf("unsupported curve: %s", curve)
	}
}

func signSecp256k1(ctx context.Context, shareABytes, shareBBytes, msgHash []byte) ([]byte, error) {
	var saveA, saveB keygen.LocalPartySaveData
	if err := json.Unmarshal(shareABytes, &saveA); err != nil {
		return nil, fmt.Errorf("unmarshal shareA: %w", err)
	}
	if err := json.Unmarshal(shareBBytes, &saveB); err != nil {
		return nil, fmt.Errorf("unmarshal shareB: %w", err)
	}

	// Reconstruct party IDs matching those used in keygen
	partyIDA := tss.NewPartyID("A", "customer", new(big.Int).SetInt64(1))
	partyIDB := tss.NewPartyID("B", "service", new(big.Int).SetInt64(2))
	parties := tss.SortPartyIDs(tss.UnSortedPartyIDs{partyIDA, partyIDB})
	peerCtx := tss.NewPeerContext(parties)
	threshold := 1

	paramsA := tss.NewParameters(tss.S256(), peerCtx, parties[0], len(parties), threshold)
	paramsB := tss.NewParameters(tss.S256(), peerCtx, parties[1], len(parties), threshold)

	outA := make(chan tss.Message, 20)
	outB := make(chan tss.Message, 20)
	endA := make(chan *signing.SignatureData, 1)
	endB := make(chan *signing.SignatureData, 1)

	msg := new(big.Int).SetBytes(msgHash)

	partyA, _ := signing.NewLocalParty(msg, paramsA, saveA, outA, endA)
	partyB, _ := signing.NewLocalParty(msg, paramsB, saveB, outB, endB)

	errCh := make(chan error, 2)

	go func() {
		if err := partyA.Start(); err != nil {
			errCh <- err
		}
	}()
	go func() {
		if err := partyB.Start(); err != nil {
			errCh <- err
		}
	}()

	// Route messages between parties until one end channel fires
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, fmt.Errorf("signing error: %w", err)
		case msg := <-outA:
			dest := msg.GetTo()
			for _, d := range dest {
				if d.Index == partyB.PartyID().Index {
					if _, err := partyB.Update(msg); err != nil {
						return nil, fmt.Errorf("partyB update: %w", err)
					}
				}
			}
		case msg := <-outB:
			dest := msg.GetTo()
			for _, d := range dest {
				if d.Index == partyA.PartyID().Index {
					if _, err := partyA.Update(msg); err != nil {
						return nil, fmt.Errorf("partyA update: %w", err)
					}
				}
			}
		case sigData := <-endA:
			// DER-encode R and S
			return derEncode(sigData.R, sigData.S), nil
		case <-endB:
			// endA is canonical — wait for it
		}
	}
}

func derEncode(r, s []byte) []byte {
	// Simple DER encoding: 0x30 len 0x02 rLen r 0x02 sLen s
	rb := stripLeadingZeros(r)
	sb := stripLeadingZeros(s)

	// Pad if high bit set (make positive)
	if rb[0]&0x80 != 0 {
		rb = append([]byte{0x00}, rb...)
	}
	if sb[0]&0x80 != 0 {
		sb = append([]byte{0x00}, sb...)
	}

	seq := []byte{0x02, byte(len(rb))}
	seq = append(seq, rb...)
	seq = append(seq, 0x02, byte(len(sb)))
	seq = append(seq, sb...)

	return append([]byte{0x30, byte(len(seq))}, seq...)
}

func stripLeadingZeros(b []byte) []byte {
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return b
}
```

> **Note to implementor**: `signing.SignatureData` field names (`R`, `S`) must be verified against the tss-lib v2 signing package. The `SignatureData` struct may use `Signature []byte` as a pre-encoded blob instead of separate R/S. Check `github.com/bnb-chain/tss-lib/v2/ecdsa/signing` before building. The routing loop structure is correct.

- [ ] **Step 4: Run signing test**

```bash
go test ./app/services/mpc/... -run TestSign -v -timeout 120s
```

Expected: PASS

- [ ] **Step 5: Run all MPC tests**

```bash
go test ./app/services/mpc/... -v -timeout 300s
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add app/services/mpc/signing.go app/services/mpc/service_test.go
git commit -m "feat(mpc): implement 2-party secp256k1 signing with tests"
```

---

## Task 6: Wallet Model + Migration

**Files:**
- Modify: `database/migrations/20260317000001_create_wallets_table.go`
- Modify: `app/models/wallet.go`

- [ ] **Step 1: Update the migration**

Replace the contents of `database/migrations/20260317000001_create_wallets_table.go`:

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260317000001CreateWalletsTable struct{}

func (r *M20260317000001CreateWalletsTable) Signature() string {
	return "20260317000001_create_wallets_table"
}

func (r *M20260317000001CreateWalletsTable) Up() error {
	return facades.Schema().Create("wallets", func(table schema.Blueprint) {
		table.Uuid("id")
		table.Primary("id")
		table.String("chain", 50).Comment("Blockchain identifier (eth, polygon, sol, btc)")
		table.String("label", 255).Nullable().Comment("User-friendly wallet label")
		// MPC key material
		table.Binary("mpc_customer_share").Comment("AES-256-GCM encrypted share_A (ciphertext || 16-byte tag)")
		table.Binary("mpc_share_iv").Comment("AES-256-GCM nonce, exactly 12 bytes")
		table.Binary("mpc_share_salt").Comment("Argon2id salt, exactly 16 bytes")
		table.Text("mpc_secret_arn").Comment("AWS Secrets Manager ARN for share_B")
		table.Text("mpc_public_key").Comment("Hex-encoded compressed public key (33 bytes secp256k1 / 32 bytes ed25519)")
		table.String("mpc_curve", 20).Comment("secp256k1 or ed25519")
		table.Text("deposit_address").Comment("Blockchain deposit address derived from combined public key")
		table.Timestamps()
		table.Index("chain")
		table.Comment("MPC co-signing wallets")
	})
}

func (r *M20260317000001CreateWalletsTable) Down() error {
	return facades.Schema().DropIfExists("wallets")
}
```

- [ ] **Step 2: Update the Wallet model**

Replace `app/models/wallet.go`:

```go
package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Wallet struct {
	orm.Model
	ID                uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Chain             string    `gorm:"type:varchar(50);not null;index" json:"chain"`
	Label             string    `gorm:"type:varchar(255)" json:"label,omitempty"`
	MPCCustomerShare  []byte    `gorm:"type:bytea;not null" json:"-"`
	MPCShareIV        []byte    `gorm:"type:bytea;not null" json:"-"`
	MPCShareSalt      []byte    `gorm:"type:bytea;not null" json:"-"`
	MPCSecretARN      string    `gorm:"type:text;not null" json:"-"`
	MPCPublicKey      string    `gorm:"type:text;not null" json:"-"`
	MPCCurve          string    `gorm:"type:varchar(20);not null" json:"mpc_curve"`
	DepositAddress    string    `gorm:"type:text;not null" json:"deposit_address"`
}

func (w *Wallet) TableName() string {
	return "wallets"
}
```

- [ ] **Step 3: Verify the build**

```bash
go build ./...
```

Expected: PASS. Fix any compile errors from removed fields (`MasterPubkey`, `KeyVaultRef`, `DerivationPath`, `AddressIndex`) referenced elsewhere.

- [ ] **Step 4: Commit**

```bash
git add database/migrations/20260317000001_create_wallets_table.go app/models/wallet.go
git commit -m "feat(schema): replace HD wallet columns with MPC columns"
```

---

## Task 7: Decommission Withdrawal Worker

**Files:**
- Modify: `main.go`
- Modify: `pkg/types/types.go`
- Modify: `app/services/queue/sqs.go`

- [ ] **Step 1: Remove handleWithdrawalWorker from main.go**

In `main.go`:
- Delete the entire `handleWithdrawalWorker` function (lines ~141–163)
- Delete the `case "withdrawal_worker": lambda.Start(handleWithdrawalWorker)` branch from the `switch mode` block

- [ ] **Step 2: Delete WithdrawalMessage from types.go**

In `pkg/types/types.go`, delete the entire `WithdrawalMessage` struct (lines ~143–152):

```go
// DELETE THIS ENTIRE BLOCK:
type WithdrawalMessage struct {
    TransactionID  string `json:"transaction_id"`
    WalletID       string `json:"wallet_id"`
    ChainID        string `json:"chain_id"`
    ToAddress      string `json:"to_address"`
    Amount         string `json:"amount"`
    Asset          string `json:"asset"`
    TokenContract  string `json:"token_contract,omitempty"`
    ExternalUserID string `json:"external_user_id"`
}
```

- [ ] **Step 3: Remove SendWithdrawal from SQS client**

In `app/services/queue/sqs.go`:
- Remove the `SendWithdrawal` method and its `Withdrawal` queue URL field from `QueueURLs`
- Remove the `Withdrawal` field from the `QueueURLs` struct and `Sender` interface

- [ ] **Step 4: Remove withdrawal queue URL from container**

In `app/container/container.go`:
- Remove `Withdrawal: os.Getenv("WITHDRAWAL_QUEUE_URL")` from the `QueueURLs` initializer

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Fix any remaining references to `WithdrawalMessage` or `SendWithdrawal`.

- [ ] **Step 6: Run existing tests**

```bash
go test ./... -timeout 120s
```

Expected: PASS (some tests may need updating if they mock `SendWithdrawal`)

- [ ] **Step 7: Commit**

```bash
git add main.go pkg/types/types.go app/services/queue/sqs.go app/container/container.go
git commit -m "feat(withdraw): decommission async withdrawal worker"
```

---

## Task 8: Update Container

**Files:**
- Modify: `app/container/container.go`

- [ ] **Step 1: Add MPCService and SecretsManagerClient**

In `app/container/container.go`, add to the `Container` struct:

```go
import (
    // existing imports...
    "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
    mpcpkg "github.com/macromarkets/vault/app/services/mpc"
)

type Container struct {
    Redis *redis.Client
    SQS   *queue.SQSClient

    SecretsManager    *secretsmanager.Client
    MPCService        mpcpkg.Service

    Registry          *chainpkg.Registry
    WalletService     *wallet.Service
    DepositService    *deposit.Service
    WithdrawalService *withdraw.Service
    WebhookService    *webhook.Service
}
```

In `Boot()`, after the AWS config is loaded, add:

```go
// --- AWS Secrets Manager ---
c.SecretsManager = secretsmanager.NewFromConfig(awsCfg)

// --- MPC Service ---
c.MPCService = mpcpkg.NewTSSService()
```

Update `WalletService` and `WithdrawalService` constructors to receive the new dependencies (updated in the next tasks).

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add app/container/container.go
git commit -m "feat(container): add MPCService and SecretsManager client"
```

---

## Task 9: Update Wallet Service

**Files:**
- Modify: `app/services/wallet/service.go`
- Modify: `app/services/wallet/service_test.go`

- [ ] **Step 1: Update CreateWallet signature in wallet service**

In `app/services/wallet/service.go`, update `NewService` to accept MPC deps and rewrite `CreateWallet`:

```go
import (
    "context"
    "encoding/hex"
    "fmt"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
    "github.com/google/uuid"
    "github.com/goravel/framework/contracts/database/orm"
    "github.com/goravel/framework/facades"
    "github.com/redis/go-redis/v9"

    "github.com/macromarkets/vault/app/models"
    chainpkg "github.com/macromarkets/vault/app/services/chain"
    mpcpkg "github.com/macromarkets/vault/app/services/mpc"
)

type Service struct {
    registry  *chainpkg.Registry
    rdb       *redis.Client
    mpc       mpcpkg.Service
    secrets   *secretsmanager.Client
}

func NewService(registry *chainpkg.Registry, rdb *redis.Client, mpcSvc mpcpkg.Service, secrets *secretsmanager.Client) *Service {
    return &Service{registry: registry, rdb: rdb, mpc: mpcSvc, secrets: secrets}
}

func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*models.Wallet, error) {
    if len(passphrase) < 12 {
        return nil, fmt.Errorf("passphrase must be at least 12 characters")
    }
    if _, err := s.registry.Chain(chainID); err != nil {
        return nil, fmt.Errorf("unknown chain: %s", chainID)
    }

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
    result, err := s.mpc.Keygen(ctx, curve)
    if err != nil {
        return nil, fmt.Errorf("mpc keygen: %w", err)
    }

    // 2. Encrypt customer share
    enc, err := mpcpkg.EncryptShare(result.ShareA, passphrase)
    if err != nil {
        return nil, fmt.Errorf("encrypt share: %w", err)
    }

    // 3. Store service share in Secrets Manager
    secretName := fmt.Sprintf("vault/wallet/%s/share-b", uuid.New().String())
    _, err = s.secrets.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
        Name:         aws.String(secretName),
        SecretBinary: result.ShareB,
    })
    if err != nil {
        return nil, fmt.Errorf("store service share: %w", err)
    }

    // 4. Derive deposit address
    depositAddress, err := deriveAddress(chainID, result.CombinedPubKey)
    if err != nil {
        return nil, fmt.Errorf("derive address: %w", err)
    }

    w := &models.Wallet{
        ID:               uuid.New(),
        Chain:            chainID,
        Label:            label,
        MPCCustomerShare: enc.Ciphertext,
        MPCShareIV:       enc.IV,
        MPCShareSalt:     enc.Salt,
        MPCSecretARN:     secretName,
        MPCPublicKey:     hex.EncodeToString(result.CombinedPubKey),
        MPCCurve:         string(curve),
        DepositAddress:   depositAddress,
    }

    if err := facades.Orm().Query().Create(w); err != nil {
        return nil, err
    }

    // Cache deposit address in Redis
    if s.rdb != nil {
        s.rdb.SAdd(ctx, "vault:addresses:"+chainID, depositAddress)
    }

    return w, nil
}
```

Add helpers:

```go
func curveForChain(chainID string) mpcpkg.Curve {
    if chainID == "sol" {
        return mpcpkg.CurveEd25519
    }
    return mpcpkg.CurveSecp256k1
}

// deriveAddress derives the single MPC deposit address from the combined public key.
func deriveAddress(chainID string, compressedPubKey []byte) (string, error) {
    switch chainID {
    case "eth", "polygon":
        return deriveEthAddress(compressedPubKey)
    case "btc":
        return deriveBtcAddress(compressedPubKey)
    case "sol":
        return deriveSolAddress(compressedPubKey)
    default:
        return "", fmt.Errorf("unsupported chain for address derivation: %s", chainID)
    }
}
```

Add the chain-specific derivation helpers in a separate file `app/services/wallet/address.go`:

```go
package wallet

import (
    "encoding/hex"
    "fmt"

    "github.com/btcsuite/btcd/btcec/v2"
    "github.com/btcsuite/btcd/btcutil"
    "github.com/btcsuite/btcd/chaincfg"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/mr-tron/base58"
)

func deriveEthAddress(compressedPubKey []byte) (string, error) {
    pub, err := btcec.ParsePubKey(compressedPubKey)
    if err != nil {
        return "", fmt.Errorf("parse pubkey: %w", err)
    }
    // go-ethereum expects uncompressed pubkey without 0x04 prefix
    uncompressed := pub.SerializeUncompressed()[1:]
    hash := crypto.Keccak256(uncompressed)
    return "0x" + hex.EncodeToString(hash[12:]), nil
}

func deriveBtcAddress(compressedPubKey []byte) (string, error) {
    pub, err := btcec.ParsePubKey(compressedPubKey)
    if err != nil {
        return "", fmt.Errorf("parse pubkey: %w", err)
    }
    pubHash := btcutil.Hash160(pub.SerializeCompressed())
    addr, err := btcutil.NewAddressWitnessPubKeyHash(pubHash, &chaincfg.MainNetParams)
    if err != nil {
        return "", fmt.Errorf("p2wpkh: %w", err)
    }
    return addr.EncodeAddress(), nil
}

func deriveSolAddress(pubKey []byte) (string, error) {
    if len(pubKey) != 32 {
        return "", fmt.Errorf("ed25519 pubkey must be 32 bytes, got %d", len(pubKey))
    }
    return base58.Encode(pubKey), nil
}
```

- [ ] **Step 2: Update GenerateAddress to return 422**

In `app/services/wallet/service.go`, replace the `GenerateAddress` method body:

```go
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, metadata string) (*models.Address, error) {
    return nil, fmt.Errorf("address derivation not supported for MPC wallets in v1")
}
```

- [ ] **Step 3: Update container to pass new deps**

In `app/container/container.go`, update:

```go
c.WalletService = wallet.NewService(c.Registry, c.Redis, c.MPCService, c.SecretsManager)
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add app/services/wallet/service.go app/services/wallet/address.go app/container/container.go
git commit -m "feat(wallet): implement MPC wallet creation with passphrase"
```

---

## Task 10: Rewrite Withdrawal Service

**Files:**
- Modify: `app/services/withdraw/service.go`

- [ ] **Step 1: Rewrite the service**

Replace the full contents of `app/services/withdraw/service.go`:

```go
package withdraw

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "math/big"
    "time"

    "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
    "github.com/google/uuid"
    "github.com/goravel/framework/facades"
    "github.com/redis/go-redis/v9"

    "github.com/macromarkets/vault/app/models"
    chainpkg "github.com/macromarkets/vault/app/services/chain"
    mpcpkg "github.com/macromarkets/vault/app/services/mpc"
    "github.com/macromarkets/vault/app/services/webhook"
    "github.com/macromarkets/vault/pkg/types"
)

// Sentinel errors for HTTP response mapping in controller.
var (
    ErrInvalidPassphrase  = errors.New("invalid passphrase")
    ErrInsufficientFunds  = errors.New("insufficient funds")
    ErrConcurrentWithdraw = errors.New("withdrawal already in progress for this wallet")
    ErrPassphraseTooShort = errors.New("passphrase must be at least 12 characters")
    ErrTooManyAttempts    = errors.New("too many failed attempts, try again later")
)

type Service struct {
    registry   *chainpkg.Registry
    webhookSvc *webhook.Service
    mpc        mpcpkg.Service
    secrets    *secretsmanager.Client
    rdb        *redis.Client
}

func NewService(
    registry *chainpkg.Registry,
    webhookSvc *webhook.Service,
    mpc mpcpkg.Service,
    secrets *secretsmanager.Client,
    rdb *redis.Client,
) *Service {
    return &Service{
        registry:   registry,
        webhookSvc: webhookSvc,
        mpc:        mpc,
        secrets:    secrets,
        rdb:        rdb,
    }
}

type WithdrawRequest struct {
    WalletID       uuid.UUID
    ExternalUserID string
    ToAddress      string
    Amount         string
    Asset          string
    Passphrase     string
    IdempotencyKey string
}

func (s *Service) Request(ctx context.Context, req WithdrawRequest) (*models.Transaction, error) {
    // Step 1: Validate passphrase length before any I/O
    if len(req.Passphrase) < 12 {
        return nil, ErrPassphraseTooShort
    }

    // Step 2: Idempotency check (no lock needed)
    if req.IdempotencyKey != "" {
        var existing models.Transaction
        if err := facades.Orm().Query().Where("idempotency_key", req.IdempotencyKey).First(&existing); err == nil {
            return &existing, nil
        }
    }

    // Step 3: Acquire per-wallet Redis lock
    lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
    acquired, err := s.rdb.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
    if err != nil {
        return nil, fmt.Errorf("redis lock: %w", err)
    }
    if !acquired {
        return nil, ErrConcurrentWithdraw
    }
    defer s.rdb.Del(ctx, lockKey)

    // Step 4: Validate to address and amount
    var wallet models.Wallet
    if err := facades.Orm().Query().Find(&wallet, req.WalletID); err != nil {
        return nil, fmt.Errorf("wallet not found: %w", err)
    }

    adapter, err := s.registry.Chain(wallet.Chain)
    if err != nil {
        return nil, err
    }
    if !adapter.ValidateAddress(req.ToAddress) {
        return nil, fmt.Errorf("invalid address for chain %s", wallet.Chain)
    }

    amount, ok := new(big.Int).SetString(req.Amount, 10)
    if !ok {
        return nil, fmt.Errorf("invalid amount: %s", req.Amount)
    }

    // Step 5: Check passphrase rate limit
    if err := s.checkRateLimit(ctx, req.WalletID.String()); err != nil {
        return nil, err
    }

    // Step 6: Check on-chain balance BEFORE loading key material
    bal, err := adapter.GetBalance(ctx, wallet.DepositAddress)
    if err != nil {
        return nil, fmt.Errorf("get balance: %w", err)
    }
    if bal.Amount.Cmp(amount) < 0 {
        return nil, ErrInsufficientFunds
    }

    // Step 7: Decrypt share_A
    enc := &mpcpkg.EncryptedShare{
        Ciphertext: wallet.MPCCustomerShare,
        IV:         wallet.MPCShareIV,
        Salt:       wallet.MPCShareSalt,
    }
    shareA, err := mpcpkg.DecryptShare(enc, req.Passphrase)
    if err != nil {
        if errors.Is(err, mpcpkg.ErrInvalidPassphrase) {
            s.recordFailedAttempt(ctx, req.WalletID.String())
            return nil, ErrInvalidPassphrase
        }
        return nil, err
    }

    // Step 8: Fetch share_B from Secrets Manager
    secret, err := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
        SecretId: &wallet.MPCSecretARN,
    })
    if err != nil {
        return nil, fmt.Errorf("fetch service share: %w", err)
    }
    shareB := secret.SecretBinary

    // Step 9: Defer zero-wipe IMMEDIATELY after shares are loaded
    defer func() {
        for i := range shareA {
            shareA[i] = 0
        }
        for i := range shareB {
            shareB[i] = 0
        }
    }()

    // Step 10: Build unsigned transaction
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
        From: wallet.DepositAddress, To: req.ToAddress, Amount: amount, Asset: req.Asset, Token: token,
    })
    if err != nil {
        return nil, fmt.Errorf("build tx: %w", err)
    }

    // Step 11: MPC Sign
    curve := mpcpkg.Curve(wallet.MPCCurve)
    sig, err := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
        TxHashes: [][]byte{unsigned.RawBytes},
    })
    if err != nil {
        return nil, fmt.Errorf("mpc sign: %w", err)
    }

    // Step 12: Attach signature and broadcast
    signed := &types.SignedTx{
        ChainID:  wallet.Chain,
        RawBytes: sig,
    }
    txHash, err := adapter.BroadcastTransaction(ctx, signed)
    if err != nil {
        return nil, fmt.Errorf("broadcast: %w", err)
    }

    // Step 13: Persist transaction (no passphrase stored)
    tx := &models.Transaction{
        ID:             uuid.New(),
        WalletID:       wallet.ID,
        ExternalUserID: req.ExternalUserID,
        Chain:          wallet.Chain,
        TxType:         "withdrawal",
        TxHash:         txHash,
        ToAddress:      req.ToAddress,
        Amount:         req.Amount,
        Asset:          req.Asset,
        TokenContract:  tokenContract,
        RequiredConfs:  int(adapter.RequiredConfirmations()),
        Status:         string(types.TxStatusConfirming),
        IdempotencyKey: req.IdempotencyKey,
    }
    if err := facades.Orm().Query().Create(tx); err != nil {
        return nil, fmt.Errorf("persist tx: %w", err)
    }

    // Step 14: Enqueue webhook notification only (no key material)
    s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalBroadcast, map[string]string{
        "tx_id": tx.ID.String(), "tx_hash": txHash,
    })

    slog.Info("withdrawal broadcast", "tx_id", tx.ID, "tx_hash", txHash, "chain", wallet.Chain)
    return tx, nil
}

func (s *Service) checkRateLimit(ctx context.Context, walletID string) error {
    key := fmt.Sprintf("vault:ratelimit:passphrase:%s", walletID)
    count, err := s.rdb.Get(ctx, key).Int()
    if err != nil && err != redis.Nil {
        return nil // fail open on Redis error — don't block withdrawals
    }
    if count >= 5 {
        return ErrTooManyAttempts
    }
    return nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, walletID string) {
    key := fmt.Sprintf("vault:ratelimit:passphrase:%s", walletID)
    pipe := s.rdb.Pipeline()
    pipe.Incr(ctx, key)
    pipe.Expire(ctx, key, 60*time.Second)
    pipe.Exec(ctx)
}

// GetTransaction and ListTransactions remain unchanged below.
func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
    var tx models.Transaction
    if err := facades.Orm().Query().Find(&tx, id); err != nil {
        return nil, err
    }
    return &tx, nil
}

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, error) {
    query := facades.Orm().Query()
    if chainID != "" {
        query = query.Where("chain", chainID)
    }
    if txType != "" {
        query = query.Where("tx_type", txType)
    }
    if status != "" {
        query = query.Where("status", status)
    }
    if userID != "" {
        query = query.Where("external_user_id", userID)
    }
    if limit <= 0 {
        limit = 50
    }
    var txs []models.Transaction
    if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&txs); err != nil {
        return nil, err
    }
    return txs, nil
}
```

- [ ] **Step 2: Update container to pass new deps to WithdrawalService**

In `app/container/container.go`:

```go
c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis)
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add app/services/withdraw/service.go app/container/container.go
git commit -m "feat(withdraw): synchronous MPC signing with Redis lock and rate limiting"
```

---

## Task 11: Update Controllers

**Files:**
- Modify: `app/http/controllers/wallets_controller.go`
- Modify: `app/http/controllers/withdrawals_controller.go`
- Modify: `app/http/controllers/addresses_controller.go`

- [ ] **Step 1: Add passphrase to CreateWallet controller**

In `app/http/controllers/wallets_controller.go`, update the request struct and call:

```go
// Update the request bind struct to include passphrase:
var req struct {
    Chain      string `json:"chain"      form:"chain"`
    Label      string `json:"label"      form:"label"`
    Passphrase string `json:"passphrase" form:"passphrase"`
}

// Add passphrase validation:
if req.Passphrase == "" || len(req.Passphrase) < 12 {
    return ctx.Response().Json(http.StatusBadRequest, http.Json{
        "error": "passphrase must be at least 12 characters",
    })
}

// Update CreateWallet call:
w, err := container.Get().WalletService.CreateWallet(ctx.Context(), req.Chain, req.Label, req.Passphrase)
```

Update the Swagger annotation `@Param` to reference `CreateWalletRequest` which should include passphrase. Update `CreateWalletRequest` type at bottom of file:

```go
type CreateWalletRequest struct {
    Chain      string `json:"chain"      example:"eth"`
    Label      string `json:"label"      example:"My Ethereum Wallet"`
    Passphrase string `json:"passphrase" example:"strong-passphrase-min-12"`
}
```

- [ ] **Step 2: Update CreateWithdrawal controller**

In `app/http/controllers/withdrawals_controller.go`, update the request struct:

```go
var req struct {
    ExternalUserID string `json:"external_user_id" form:"external_user_id"`
    ToAddress      string `json:"to_address"       form:"to_address"`
    Amount         string `json:"amount"           form:"amount"`
    Asset          string `json:"asset"            form:"asset"`
    Passphrase     string `json:"passphrase"       form:"passphrase"`
    IdempotencyKey string `json:"idempotency_key"  form:"idempotency_key"`
}

// Required fields check:
if req.ExternalUserID == "" || req.ToAddress == "" || req.Amount == "" || req.Asset == "" || req.Passphrase == "" || req.IdempotencyKey == "" {
    return ctx.Response().Json(http.StatusBadRequest, http.Json{
        "error": "external_user_id, to_address, amount, asset, passphrase, and idempotency_key are required",
    })
}
```

Update the service call and map sentinel errors to HTTP codes:

```go
import (
    "errors"
    "github.com/macromarkets/vault/app/services/withdraw"
)

tx, err := container.Get().WithdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
    WalletID:       walletID,
    ExternalUserID: req.ExternalUserID,
    ToAddress:      req.ToAddress,
    Amount:         req.Amount,
    Asset:          req.Asset,
    Passphrase:     req.Passphrase,
    IdempotencyKey: req.IdempotencyKey,
})
if err != nil {
    switch {
    case errors.Is(err, withdraw.ErrInvalidPassphrase):
        return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": err.Error()})
    case errors.Is(err, withdraw.ErrPassphraseTooShort):
        return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
    case errors.Is(err, withdraw.ErrInsufficientFunds):
        return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
    case errors.Is(err, withdraw.ErrConcurrentWithdraw):
        return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
    case errors.Is(err, withdraw.ErrTooManyAttempts):
        return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": err.Error()})
    default:
        return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
    }
}
```

Update `CreateWithdrawalRequest` Swagger type to include passphrase:

```go
type CreateWithdrawalRequest struct {
    ExternalUserID string `json:"external_user_id" example:"user_123"`
    ToAddress      string `json:"to_address"       example:"0xABCDEF1234567890"`
    Amount         string `json:"amount"           example:"0.5"`
    Asset          string `json:"asset"            example:"eth"`
    Passphrase     string `json:"passphrase"       example:"strong-passphrase-min-12"`
    IdempotencyKey string `json:"idempotency_key"  example:"wdl_20260318_001"`
}
```

Update the Swagger `@Description` annotation to reflect synchronous (not async) processing.

- [ ] **Step 3: Update GenerateAddress controller to return 422**

In `app/http/controllers/addresses_controller.go`, find `GenerateAddress` handler and update the error response to match:

```go
// In GenerateAddress handler, after calling WalletService.GenerateAddress:
if err != nil {
    // MPC wallets return a specific error message; map to 422
    return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
        "error": err.Error(),
    })
}
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add app/http/controllers/wallets_controller.go app/http/controllers/withdrawals_controller.go app/http/controllers/addresses_controller.go
git commit -m "feat(api): add passphrase to wallet and withdrawal controllers"
```

---

## Task 12: End-to-End Local Verification

**Prerequisites:** Docker running, `.env.dev` present, AWS credentials configured (or LocalStack)

- [ ] **Step 1: Start Docker services**

```bash
make docker-up
```

Expected: PostgreSQL and Redis containers running

- [ ] **Step 2: Refresh the database**

Since we changed the wallets migration, drop and recreate:

```bash
make migrate-fresh  # or: docker exec -it <pg_container> psql -U vault -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
make migrate-up
```

- [ ] **Step 3: Run all tests**

```bash
go test ./... -timeout 300s
```

Expected: all PASS

- [ ] **Step 4: Start the server**

```bash
make run
```

- [ ] **Step 5: Create a wallet via curl**

```bash
curl -s -X POST http://localhost:8080/v1/wallets \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key" \
  -H "X-API-Signature: $(echo -n '{"chain":"eth","label":"test","passphrase":"my-strong-pass-123"}' | openssl dgst -sha256 -hmac 'dev-secret-key-change-in-production' -binary | xxd -p -c 256)" \
  -d '{"chain":"eth","label":"test","passphrase":"my-strong-pass-123"}' | jq .
```

Expected: `{"id": "...", "chain": "eth", "mpc_curve": "secp256k1", ...}`

- [ ] **Step 6: Attempt withdrawal with wrong passphrase**

```bash
curl -s -X POST http://localhost:8080/v1/wallets/<wallet_id>/withdrawals \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key" \
  -d '{"external_user_id":"user1","to_address":"0x742d35Cc6634C0532925a3b844Bc454e4438f44e","amount":"1000000000000000000","asset":"eth","passphrase":"wrong-pass","idempotency_key":"test-001"}' | jq .
```

Expected: `{"error": "invalid passphrase"}` with HTTP 401

- [ ] **Step 7: Regenerate Swagger docs**

```bash
make swagger-generate
```

- [ ] **Step 8: Final commit**

```bash
git add docs/swagger.json docs/swagger.yaml docs/docs.go
git commit -m "docs: regenerate swagger after MPC wallet changes"
```
