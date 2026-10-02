# Essential Custody Gaps Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Replace example commands with this repo’s real tools (package manager, test runner, linter).

**Goal:** This effort runs two tracks in parallel. Track Wallets: Macro Wallets can derive an address, detect a deposit, and broadcast a withdrawal for ETH, POL, BTC, SOL, and for USDT/USDC on `eth`, `polygon`, and `sol`. SOL deposit detection is the Helius webhook. `SolanaLive.ScanBlock` stays unchanged and still omits `To`. Track Markets: `settings/crypto-custody`, feature flags, `WalletProviderFactory`, the `crypto_custody_wallets` rename, `wallet_addresses.provider`, and both webhooks stay live.

**Architecture:** Track Wallets edits `macro-wallets/back`. Polygon native asset is `POL`. There is no MATIC alias. Token amounts use seed decimals. SOL and BTC withdrawals sign with a reconstructed private key inside `SignTransaction`; EVM keeps `mpc.Sign` plus `FinalizeMPCSignature`. Track Markets (Section B) edits `macro-markets/back` in the same effort: factory, flags, settings page, table rename, address provider, and dual webhooks. The tracks share no code.

**Tech Stack:** Go 1.25, Goravel, `github.com/btcsuite/btcd` (already required), `github.com/gagliardetto/solana-go` (added in Task 6). Unit tests: `go test ./<package> -count=1`. Full suite: `make test` from `macro-wallets/back`. Markets tests are Pest (`php artisan test` on the files named in each Section B task). Section D is Pest feature tests plus the Goravel process those tests call over HTTP.

**Spec:** `docs/superpowers/specs/2026-09-29-crypto-custody-provider-design.md`

**Parallel execution:** Track Wallets and Track Markets have no shared code. Dispatch them as separate subagents. Do not wait for Wallets signing work before starting the Markets factory and settings page. The shared contract they both rely on is already in the spec (external API `/api/v1`, the `asset` field, provider values `bitgo`|`macro_wallets`). Use Cursor Multitask Mode and subagent-driven development: one subagent per independent task, with review between tasks.

**Section C (other chains) stays out of this effort.**

**Section D is the cross-repo gate.** Tasks 1–17 stay as they are. Neither track is done until the Section D integration test and e2e scenarios pass.

---

## File map

### Track Wallets — create

- `app/services/withdraw/asset.go` — resolve requested asset to wallet symbol plus base units.
- `app/services/withdraw/asset_test.go`
- `app/services/chain/solana_token.go` — ATA derivation and `GetTokenBalance`.
- `app/services/chain/solana_token_test.go`
- `app/services/chain/solana_tx.go` — build, sign, and broadcast Solana transactions.
- `app/services/chain/solana_tx_test.go`
- `app/services/chain/bitcoin_tx.go` — UTXO selection, P2WPKH sign, REST broadcast.
- `app/services/chain/bitcoin_tx_test.go`

### Track Wallets — modify

- `app/models/chain.go` — native symbols, token symbols, and the USDT/USDC contracts and mints, in the existing chain-id const block. No second file.
- `app/services/chain/registry.go` — `FindTokenByContract`.
- `app/services/chain/registry_test.go`
- `app/services/chain/solana.go` — replace `GetTokenBalance`, `BuildTransfer`, `SignTransaction`, `BroadcastTransaction`, `BuildSweep` bodies by delegating to the new files. Leave `ScanBlock` unchanged.
- `app/services/chain/bitcoin.go` — replace `SignTransaction`, `BroadcastTransaction`, `BuildTransfer`, `BuildSweep` by delegating. Leave `ScanBlock` unchanged.
- `app/services/mpc/service.go` — add `ReconstructSecp256k1PrivateKey` to `Service`.
- `app/services/mpc/signing.go` — implement it.
- `tests/mocks/mpc.go` — stub the new method.
- `app/http/requests/create_wallet_withdrawal_request.go` — optional `asset`.
- `app/http/controllers/wallet_withdrawals_controller.go` — use resolved asset and decimals. Swagger struct gains `asset`.
- `app/services/ingest/providers/alchemy.go` — token fallback uses payload decimals, marks human-scaled amounts.
- `app/services/ingest/providers/helius.go` — keep the human SPL amount when decimals are omitted.
- `app/services/ingest/providers/provider.go` — fields the service needs to rescale.
- `app/services/ingest/service.go` — registry symbol and decimals.
- `app/services/deposit/service.go` — persist the token symbol or the native asset. Polygon native is `POL`.
- `app/services/sweep/planner.go` — allow `solana` and `bitcoin`; no gas seed on those chains.
- `app/services/sweep/consolidate.go` — same gate.
- `app/services/sweep/executor.go` — local sign for `sol`/`tsol`/`btc`/`tbtc`.
- `app/services/withdraw/service.go` — do not require `GasStatusSeeded` when the chain has no gas threshold.
- Matching `*_test.go` files listed in each task.

### Track Markets (`/home/raphaelcangucu/macro-markets/back`)

- Modify: `app/Models/Currency.php` — add `PROVIDER_BITGO`, `PROVIDER_MACRO_WALLETS`, `CHAIN_ETH`, `CHAIN_POLYGON`, `CHAIN_BTC`, and `CHAIN_SOL` beside the existing `ETH`, `POL`, `BTC`, `SOL`, `USDT`, and `USDC` constants. Do not add a new class. Do not copy those provider constants onto `BitgoWallet`.

See Section B. Those files start in this effort, in parallel with Track Wallets.

### Section D — create (tests only)

- `macro-markets/back/tests/Feature/CryptoCustody/MacroWalletsHttpContractTest.php`
- `macro-wallets/back/app/services/webhook/deliver_markets_integration_test.go`
- `macro-markets/back/tests/Feature/Filament/ManageCryptoCustodyE2ETest.php`
- `macro-markets/back/tests/Feature/CryptoCustody/DepositAddressMacroWalletsE2ETest.php`
- `macro-markets/back/tests/Feature/CryptoCustody/DepositAddressBitGoE2ETest.php`
- `macro-markets/back/tests/Feature/CryptoCustody/CustodyWebhookRetrocompatE2ETest.php`

---

## Section A — Track Wallets (Macro Wallets)

### Task 1: Official asset constants

**Files:**
- Modify: `app/models/chain.go`

Chain ids already exist in this file: `ChainETH`, `ChainPolygon`, `ChainSOL`, `ChainBTC`, `ChainTETH`, `ChainTPolygon`, `ChainTBTC`, `ChainTSOL`. Extend that same `const` block. Do not add `app/services/chain/assets.go` or any second list. Do not use `ChainMatic` and do not add a MATIC-to-POL mapping. Leave the existing `ChainMatic` constant in place.

JSON payload fixtures and struct-tag examples stay string literals. They are external payloads or compile-time tags. Go that compares or assigns an official asset, chain id, token symbol, or contract uses the constants below. A negative test for an asset outside this slice (SHIB) keeps that literal.

- [ ] **Step 1: Add the constants**

```go
NativeETH = "eth"
NativePOL = "POL"
NativeBTC = "btc"
NativeSOL = "sol"

SymbolUSDT = "USDT"
SymbolUSDC = "USDC"

USDTContractETH     = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
USDCContractETH     = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
USDTContractPolygon = "0xc2132D05D31c914a87C6611C10748AEb04B58e8F"
USDCContractPolygon = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
USDTMintSOL         = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
USDCMintSOL         = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
```

`NativeETH` and `ChainETH` both spell `eth`. Call sites that mean the wallet symbol use `NativeETH`. Call sites that mean the chain id use `ChainETH`. Polygon native is `NativePOL`, not `ChainPolygon`.

- [ ] **Step 2: Compile the package**

Run: `go test ./app/models/ -count=1`

Expected: PASS. This task only names values that already exist.

- [ ] **Step 3: Commit**

```bash
git add app/models/chain.go
git commit -m "feat: name official chain, asset, and token constants"
```

### Task 2: Registry lookup by contract

**Files:**
- Modify: `app/services/chain/registry.go`
- Modify: `app/services/chain/registry_test.go`

- [ ] **Step 1: Write the failing test**

Append to `registry_test.go`. Import `strings` and `github.com/macrowallets/waas/app/models`.

```go
func TestFindTokenByContract_CaseInsensitiveEVM(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{
		Symbol: models.SymbolUSDT, ChainID: models.ChainETH, Decimals: 6,
		Contract: models.USDTContractETH,
	})
	got, err := r.FindTokenByContract(models.ChainETH, strings.ToLower(models.USDTContractETH))
	if err != nil {
		t.Fatal(err)
	}
	if got.Symbol != models.SymbolUSDT || got.Decimals != 6 {
		t.Fatalf("got %+v", got)
	}
}

func TestFindTokenByContract_SolanaMintExact(t *testing.T) {
	r := NewRegistry()
	r.RegisterToken(types.Token{
		Symbol: models.SymbolUSDC, ChainID: models.ChainSOL, Decimals: 6,
		Contract: models.USDCMintSOL,
	})
	if _, err := r.FindTokenByContract(models.ChainSOL, strings.ToLower(models.USDCMintSOL)); err == nil {
		t.Fatal("sol mint match must stay case-sensitive")
	}
	got, err := r.FindTokenByContract(models.ChainSOL, models.USDCMintSOL)
	if err != nil || got.Decimals != 6 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/chain/ -count=1 -run TestFindTokenByContract`

Expected: FAIL, `r.FindTokenByContract undefined`

- [ ] **Step 3: Write minimal implementation**

Add to `registry.go`:

```go
import "strings"

// FindTokenByContract returns the seeded token for this chain.
// EVM contracts match case-insensitively. Solana mints match exactly.
func (r *Registry) FindTokenByContract(chainID, contract string) (*types.Token, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	want := strings.TrimSpace(contract)
	evm := strings.HasPrefix(strings.ToLower(want), "0x")
	for _, t := range r.tokens[chainID] {
		if evm {
			if strings.EqualFold(t.Contract, want) {
				copy := t
				return &copy, nil
			}
			continue
		}
		if t.Contract == want {
			copy := t
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("token contract %s not found on chain %s", contract, chainID)
}
```

Keep the existing `strings` import if you add `"strings"` next to `"fmt"`. The function returns a copy so callers cannot mutate the registry slice element.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run 'TestFindToken|TestRegistry'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/services/chain/registry.go app/services/chain/registry_test.go
git commit -m "feat: look up seeded tokens by contract"
```

### Task 3: Withdrawal asset and token decimals

**Files:**
- Create: `app/services/withdraw/asset.go`
- Test: `app/services/withdraw/asset_test.go`
- Modify: `app/http/requests/create_wallet_withdrawal_request.go`
- Modify: `app/http/controllers/wallet_withdrawals_controller.go` (the decimals block at lines 151-154 and `Asset: adapter.NativeAsset()` at lines 183 and 233)
- Modify: `CreateWalletWithdrawalSwagger` in the same controller file

`humanToBaseUnits` already lives in the controller and rejects amounts with more fractional digits than `decimals`. USDT `1.5` must become `1500000`, not `1.5 * 10^18`.

- [ ] **Step 1: Write the failing test**

```go
package withdraw

import (
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestResolveWithdrawalAmount_USDTUses6Decimals(t *testing.T) {
	tokens := []types.Token{{
		Symbol: models.SymbolUSDT, ChainID: models.ChainETH, Decimals: 6,
		Contract: models.USDTContractETH,
	}}
	got, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, models.SymbolUSDT, "1.5", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if got.WalletAsset != models.SymbolUSDT || got.BaseUnits.Cmp(big.NewInt(1_500_000)) != 0 {
		t.Fatalf("%+v", got)
	}
	if got.Token == nil || got.Token.Decimals != 6 {
		t.Fatal("expected token")
	}
}

func TestResolveWithdrawalAmount_OmittedAssetIsNative(t *testing.T) {
	got, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "0.001", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Int).SetString("1000000000000000", 10)
	if got.WalletAsset != models.NativeETH || got.BaseUnits.Cmp(want) != 0 || got.Token != nil {
		t.Fatalf("%+v", got)
	}
}

func TestResolveWithdrawalAmount_POLIsNative(t *testing.T) {
	got, err := ResolveWithdrawalAmount(models.ChainPolygon, models.NativePOL, 18, models.NativePOL, "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Int).SetString("1000000000000000000", 10)
	if got.WalletAsset != models.NativePOL || got.BaseUnits.Cmp(want) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestResolveWithdrawalAmount_UnknownAsset(t *testing.T) {
	_, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "SHIB", "1", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/withdraw/ -count=1 -run TestResolveWithdrawalAmount`

Expected: FAIL, `undefined: ResolveWithdrawalAmount`

- [ ] **Step 3: Write minimal implementation**

`app/services/withdraw/asset.go`:

```go
package withdraw

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/pkg/types"
)

type ResolvedWithdrawal struct {
	WalletAsset string
	BaseUnits   *big.Int
	Token       *types.Token
}

// ResolveWithdrawalAmount converts a human amount into base units.
// Empty asset means the chain native asset. Any other asset must match a
// seeded token symbol, case-insensitively, and uses that token's decimals.
// Polygon native is POL. There is no MATIC alias.
func ResolveWithdrawalAmount(chainID, nativeSymbol string, nativeDecimals int, requested, human string, tokens []types.Token) (*ResolvedWithdrawal, error) {
	req := strings.TrimSpace(requested)
	if req == "" || strings.EqualFold(req, nativeSymbol) {
		base, err := humanToBaseUnits(human, nativeDecimals)
		if err != nil {
			return nil, err
		}
		return &ResolvedWithdrawal{WalletAsset: nativeSymbol, BaseUnits: base}, nil
	}
	var match *types.Token
	for i := range tokens {
		if strings.EqualFold(tokens[i].Symbol, req) {
			copy := tokens[i]
			match = &copy
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("unknown asset %s on chain %s", req, chainID)
	}
	base, err := humanToBaseUnits(human, int(match.Decimals))
	if err != nil {
		return nil, err
	}
	return &ResolvedWithdrawal{WalletAsset: match.Symbol, BaseUnits: base, Token: match}, nil
}

func humanToBaseUnits(human string, decimals int) (*big.Int, error) {
	if decimals < 0 || decimals > 36 {
		return nil, fmt.Errorf("invalid decimals")
	}
	trimmed := strings.TrimSpace(human)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is required")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid amount")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if strings.TrimLeft(whole, "0123456789") != "" || strings.TrimLeft(frac, "0123456789") != "" {
		return nil, fmt.Errorf("invalid amount")
	}
	if len(frac) > decimals {
		return nil, fmt.Errorf("amount has too many decimal places")
	}
	frac += strings.Repeat("0", decimals-len(frac))
	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	value, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}
	return value, nil
}
```

Move is a copy of the controller helper. After this task the controller calls `withdraw.ResolveWithdrawalAmount` and deletes its private `humanToBaseUnits` only if nothing else in the file calls it. `Estimate` paths in the same file that still need the helper should call `withdraw` as well, or keep a thin wrapper. Do not leave two copies: delete the controller function and update its callers in this file to `withdraw`’s exported path. If a caller is unexported-test-only, update that test.

Request struct, add the field and do not add it to `Rules` (omitted means native):

```go
Asset string `form:"asset" json:"asset,omitempty"`
```

Swagger:

```go
Asset string `json:"asset,omitempty" example:"USDT"`
```

The `example` tag stays a string literal. Struct tags cannot reference `models.SymbolUSDT`.

In `CreateWalletWithdrawal`, replace the `decimals := 18` block and both `adapter.NativeAsset()` uses:

```go
chainEntity, chainErr := container.Get().ChainRepo.FindByID(wallet.Chain)
if chainErr != nil || chainEntity == nil {
	return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": "chain not found"})
}
resolved, resolveErr := withdraw.ResolveWithdrawalAmount(
	wallet.Chain,
	adapter.NativeAsset(),
	chainEntity.NativeDecimals,
	req.Asset,
	req.Amount,
	container.Get().Registry.TokensForChain(wallet.Chain),
)
if resolveErr != nil {
	return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": resolveErr.Error()})
}
```

Fee estimate and `WithdrawRequest.Asset` use `resolved.WalletAsset`. `Amount` on `WithdrawRequest` is `resolved.BaseUnits.String()`. When `resolved.Token != nil`, the fee estimate `TransferRequest` sets `Token: resolved.Token` and `Asset: resolved.WalletAsset`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/services/withdraw/ -count=1 -run TestResolveWithdrawalAmount`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/services/withdraw/asset.go app/services/withdraw/asset_test.go \
  app/http/requests/create_wallet_withdrawal_request.go \
  app/http/controllers/wallet_withdrawals_controller.go
git commit -m "feat: withdraw tokens with their own decimals"
```

### Task 4: Webhook symbol and decimals

**Files:**
- Modify: `app/services/ingest/providers/provider.go`
- Modify: `app/services/ingest/providers/alchemy.go` (`parseAmount`, `activityToTransfer`)
- Modify: `app/services/ingest/providers/helius.go` (SPL loop)
- Modify: `app/services/ingest/providers/alchemy_test.go`
- Modify: `app/services/ingest/providers/helius_test.go`
- Modify: `app/services/ingest/service.go` (`processTransfer` asset block, lines 78-85)
- Modify: `app/services/ingest/service_test.go`
- Modify: `app/services/deposit/service.go` (asset assignment, lines 142-147)

EVM `scanERC20` already sets symbol from `a.cfg.ERC20Tokens`. Do not edit `evm.go` for this task.

- [ ] **Step 1: Write the failing tests**

Add to `helius_test.go`:

```go
func TestHeliusParsePayload_SPLOmitsDecimalsKeepsHumanAmount(t *testing.T) {
	payload := []byte(`[{
		"signature": "sig",
		"slot": 1,
		"tokenTransfers": [{
			"fromUserAccount": "FromWalletAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			"toUserAccount": "ToWalletBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			"tokenAmount": 1.5,
			"mint": "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
		}]
	}]`)
	transfers, err := NewHeliusProvider("k").ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	tx := transfers[0]
	if tx.HumanAmount != "1.5" || !tx.AmountIsHuman {
		t.Fatalf("%+v", tx)
	}
	if tx.Token == nil || tx.Token.Symbol != "" {
		t.Fatalf("symbol must stay empty until registry resolve, got %+v", tx.Token)
	}
}
```

The JSON above is an external Helius webhook body. The mint stays a string literal inside that payload.

Add to `alchemy_test.go` a payload with `category: token`, `value: 1.5`, empty `rawValue`, `decimals: 18`, contract `models.USDTContractETH`. The JSON body is an external Alchemy payload and keeps the contract as a string literal. Assert `AmountIsHuman` is true and `HumanAmount` is `1.5`. The parser must not scale by 18. A payload with `rawValue` `0x5f5e100` keeps `AmountIsHuman` false and `Amount` equal to that hex integer.

Add to `service_test.go` a case that feeds an `InboundTransfer` with `AmountIsHuman: true`, `HumanAmount: "1.5"`, `Token.Contract` set to `models.USDTContractETH`, `Token.Symbol` empty, and a registry containing that token at 6 decimals. Assert the created transaction `Asset` is `models.SymbolUSDT` and `Amount` is `1500000`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/ingest/... ./app/services/deposit/ -count=1 -run 'TestHeliusParsePayload_SPLOmits|TestParsePayload_ERC20|TestProcess'`

Expected: FAIL compiling on `AmountIsHuman` or assertion mismatch (`Asset` still empty)

- [ ] **Step 3: Write minimal implementation**

`InboundTransfer` gains:

```go
AmountIsHuman bool
HumanAmount   string
```

Alchemy `activityToTransfer`: when `category == token` and `rawValue` is empty, set `AmountIsHuman` true and `HumanAmount` from `value` (`strconv.FormatFloat(act.Value, 'f', -1, 64)`). Do not scale by 18 or by payload decimals. When `rawValue` is present, keep the hex integer and `AmountIsHuman` false. Still do not set `Token.Symbol`. The service scales `HumanAmount` with the seed decimals, so a payload that says 18 decimals cannot credit USDT as wei.

Helius SPL loop: if `Decimals == nil`, set `AmountIsHuman` true, `HumanAmount` via `strconv.FormatFloat(tt.TokenAmount, 'f', -1, 64)`, `Amount` nil, `Token.Decimals` 0. If `Decimals != nil`, keep `floatHumanToRawBigInt`. `Asset` stays `spl` until the service resolves it. `Token.Symbol` stays empty.

`ingest.Service.processTransfer`, replace the asset block:

```go
asset := adapter.NativeAsset()
var tokenContract string
if transfer.Token != nil {
	seeded, findErr := s.registry.FindTokenByContract(chainID, transfer.Token.Contract)
	if findErr != nil {
		return nil
	}
	asset = seeded.Symbol
	tokenContract = seeded.Contract
	transfer.Token.Symbol = seeded.Symbol
	transfer.Token.Decimals = seeded.Decimals
	if transfer.AmountIsHuman {
		base, convErr := withdraw.ResolveWithdrawalAmount(chainID, adapter.NativeAsset(), 0, seeded.Symbol, transfer.HumanAmount, []types.Token{*seeded})
		if convErr != nil {
			return convErr
		}
		transfer.Amount = base.BaseUnits
	}
} else if transfer.Asset != "" {
	asset = transfer.Asset
}
if transfer.Amount == nil {
	return fmt.Errorf("missing amount")
}
tx.Asset = asset
```

Call `withdraw.ResolveWithdrawalAmount` only for the human-scaled path. Passing `nativeDecimals: 0` is safe because the matched symbol is the token, not native. Import cycle check: `ingest` may import `withdraw` only if `withdraw` does not import `ingest`. It does not. If a cycle appears, move `humanToBaseUnits` into `app/services/chain/amount.go` and call that from both packages instead.

`deposit.Service.processTransfer`: after choosing `asset` from the token symbol or `adapter.NativeAsset()`, set `tx.Asset` to that value. Polygon native is `models.NativePOL`, the same symbol `NativeAsset` returns. There is no MATIC alias.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/services/ingest/... ./app/services/deposit/ ./app/services/chain/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/services/ingest app/services/deposit/service.go app/services/deposit/service_test.go
git commit -m "fix: credit ERC-20 and SPL deposits with seed decimals"
```

### Task 5: Solana token balance

**Files:**
- Create: `app/services/chain/solana_token.go`
- Test: `app/services/chain/solana_token_test.go`
- Modify: `app/services/chain/solana.go` `GetTokenBalance` to call the new function

**Done when:** `GetTokenBalance` for `models.USDCMintSOL` and owner `7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV` queries `getTokenAccountBalance` on the ATA and returns raw amount `1500000`, decimals 6, asset `models.SymbolUSDC`. A JSON-RPC error whose message contains `could not find account` returns amount 0 and a nil error.

- [ ] **Step 1: Add the library**

Run: `go get github.com/gagliardetto/solana-go@v1.12.0`

Expected: `go.mod` and `go.sum` list the module.

- [ ] **Step 2: Write the failing test**

Use `httptest.NewServer`. The JSON-RPC body method must be `getTokenAccountBalance`. Params[0] must equal the ATA derived by `solana.FindAssociatedTokenAddress`. Respond:

```json
{"jsonrpc":"2.0","id":1,"result":{"value":{"amount":"1500000","decimals":6,"uiAmount":1.5,"uiAmountString":"1.5"}}}
```

Assert balance amount `1500000`, asset `models.SymbolUSDC`, decimals 6. The JSON-RPC body above stays a string literal. It is an external RPC payload.

Second test: RPC error `{"code":-32602,"message":"could not find account"}` yields amount 0 and nil error.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./app/services/chain/ -count=1 -run TestSolanaGetTokenBalance`

Expected: FAIL, current stub error `SOL token balance not implemented`

- [ ] **Step 4: Write minimal implementation**

```go
func (a *SolanaLive) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	owner, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return nil, fmt.Errorf("sol owner: %w", err)
	}
	mint, err := solana.PublicKeyFromBase58(token.Contract)
	if err != nil {
		return nil, fmt.Errorf("sol mint: %w", err)
	}
	ata, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	if err != nil {
		return nil, fmt.Errorf("sol ata: %w", err)
	}
	var result struct {
		Value struct {
			Amount   string `json:"amount"`
			Decimals uint8  `json:"decimals"`
		} `json:"value"`
	}
	callErr := a.rpc.Call(ctx, "getTokenAccountBalance", &result, ata.String(), map[string]string{"commitment": "finalized"})
	if callErr != nil {
		if strings.Contains(strings.ToLower(callErr.Error()), "could not find account") {
			return &types.Balance{Address: address, Asset: token.Symbol, Amount: big.NewInt(0), Decimals: token.Decimals, Human: "0"}, nil
		}
		return nil, callErr
	}
	amt, ok := new(big.Int).SetString(result.Value.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("sol token amount %q", result.Value.Amount)
	}
	dec := token.Decimals
	if dec == 0 {
		dec = result.Value.Decimals
	}
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: amt, Decimals: dec, Human: fmtUnits(amt, dec)}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run TestSolanaGetTokenBalance`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum app/services/chain/solana.go app/services/chain/solana_token.go app/services/chain/solana_token_test.go
git commit -m "feat: read Solana SPL balances from the associated token account"
```

### Task 6: Solana sign and broadcast

**Files:**
- Create: `app/services/chain/solana_tx.go`
- Test: `app/services/chain/solana_tx_test.go`
- Modify: `app/services/chain/solana.go` so `BuildTransfer`, `SignTransaction`, `BroadcastTransaction`, and `BuildSweep` call the new file

**Done when:**

- Native: `BuildTransfer` puts the latest blockhash and a system-transfer instruction in `UnsignedTx`. `SignTransaction` with a 32-byte ed25519 seed returns a 64-byte-signature transaction whose first signature verifies with `crypto/ed25519`.
- SPL: `BuildTransfer` with `Token` set transfers `Amount` from the source ATA to the destination ATA. `BuildTransfer` calls `getAccountInfo` on the destination ATA. When the error contains `could not find account`, the unsigned transaction also creates that ATA. Metadata key `dest_ata_exists` is `false` in that case and `true` when the account is present. Fee payer is `From`.
- `BroadcastTransaction` calls JSON-RPC `sendTransaction` with base64 encoding and returns the signature string.
- `BuildSweep` returns one unsigned transaction, never `ErrUnsupportedChain`. Native sweep sends `req.Amount` if set, otherwise `NativeBalance - 5000`. If that difference is `<= 0`, return `insufficient native for fee`. SPL sweep sends `req.Amount` and returns `insufficient native for fee` when `NativeBalance < 5000`.

`ScanBlock` is not modified.

- [ ] **Step 1: Write the failing native sign test**

Import `github.com/macrowallets/waas/app/models`.

```go
func TestSolanaSignNative(t *testing.T) {
	seed := bytes.Repeat([]byte{0x07}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	from := solana.PublicKeyFromBytes(priv.Public().(ed25519.PublicKey))
	to := solana.MustPublicKeyFromBase58("7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV")
	var blockhash solana.Hash
	blockhash[0] = 1
	raw, err := buildSolanaNativeTx(from, to, 1000, blockhash.String())
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signSolanaTx(&types.UnsignedTx{ChainID: models.ChainSOL, RawBytes: raw}, seed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := solana.TransactionFromBytes(signed.RawBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.VerifySignatures(); err != nil {
		t.Fatal(err)
	}
}
```

`buildSolanaNativeTx` and `signSolanaTx` are the unexported functions `SolanaLive.BuildTransfer` and `SignTransaction` call. The test fails until those functions exist.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/chain/ -count=1 -run TestSolanaSignNative`

Expected: FAIL, `SOL signing not implemented`

- [ ] **Step 3: Implement native build, sign, and broadcast**

`BuildTransfer` calls `getLatestBlockhash` through `a.rpc` (the method already used in `solana.go`) and then `buildSolanaNativeTx` or `buildSolanaSPLTx`.

`SignTransaction` reads `unsigned.RawBytes` as the serialized **message** (not a signed tx). It builds `solana.PrivateKey` from `ed25519.NewKeyFromSeed(privateKey)` which is 64 bytes. It rejects `len(privateKey) != 32`. It signs that message and returns `SignedTx.RawBytes` as the full serialized transaction and `TxHash` as the base58 signature.

`BroadcastTransaction` base64-encodes `signed.RawBytes` and calls `sendTransaction`. Empty bytes return `sol broadcast: empty signed transaction`.

Zero the seed copy after signing.

- [ ] **Step 4: Run native test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run 'TestSolanaSignNative|TestSolanaBroadcast'`

Expected: PASS. Broadcast test uses `httptest` and asserts method `sendTransaction` and a returned signature `sig123`.

- [ ] **Step 5: Write the failing SPL and sweep tests**

SPL: owner and mint from Task 5. Amount `1500000`. Assert the signed transaction contains a `Transfer` instruction whose amount bytes are little-endian `1500000`, or assert `buildSolanaSPLTx` instruction program id is the SPL token program `TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA`.

Sweep native: `NativeBalance` 6000, `Amount` nil, expect a transfer of 1000 lamports. `NativeBalance` 4000, `Amount` nil, expect error `insufficient native for fee`.

Sweep SPL: `NativeBalance` 4999, expect `insufficient native for fee`. `NativeBalance` 5000 and `Amount` 10, expect one unsigned tx and nil error.

- [ ] **Step 6: Implement SPL build and BuildSweep**

Use `solana.FindAssociatedTokenAddress` and `token.NewTransferInstruction`. `BuildTransfer` calls `getAccountInfo` on the destination ATA. If the RPC error contains `could not find account`, prepend `associatedtokenaccount.NewCreateInstruction` and set metadata `dest_ata_exists` to false. If the account exists, set `dest_ata_exists` to true and do not prepend the create instruction. `BuildSweep` for SPL uses that same check.

`BuildSweep` for native and SPL returns `[]types.UnsignedTx{*unsigned}` of length 1.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run 'TestSolana'`

Expected: PASS, including existing `TestSolana_ValidateAddress`

- [ ] **Step 8: Commit**

```bash
git add app/services/chain/solana.go app/services/chain/solana_tx.go app/services/chain/solana_tx_test.go
git commit -m "feat: sign and broadcast Solana native and SPL transfers"
```

### Task 7: Bitcoin sign and REST broadcast

**Files:**
- Create: `app/services/chain/bitcoin_tx.go`
- Test: `app/services/chain/bitcoin_tx_test.go`
- Modify: `app/services/chain/bitcoin.go` `BuildTransfer`, `SignTransaction`, `BroadcastTransaction`, `BuildSweep`

Live addresses are P2WPKH (`addressing.DeriveBtcAddress`, witness version 0). `GetTokenBalance` stays the error `bitcoin does not support tokens`.

**Done when:**

- `SignTransaction` accepts a 32-byte secp256k1 key and an unsigned tx whose metadata lists inputs `{txid, vout, value}` and outputs `{address, value}`. The returned `RawBytes` decode with `wire.MsgTx.Deserialize` and input 0 has a witness.
- `BuildTransfer` lists UTXOs. REST uses `GET {base}/address/{addr}/utxo` (same host logic as `getBalanceREST`). RPC uses `listunspent`. It selects confirmed UTXOs greedily until `sum >= amount + fee`. Fee is `140 * feeRate` satoshis. `feeRate` is `cfg.FeeRateDefault` when `> 0`, otherwise `10`. Change goes back to `From` when `sum - amount - fee >= 546`. Below that, change is added to the fee and there is one output. Not enough confirmed value returns `insufficient funds`.
- `BuildSweep` spends every confirmed UTXO to `req.To` for `req.Amount` when set, using the same fee rule. It returns one transaction.
- `BroadcastTransaction` on `restAPI` `POST`s the hex to `{base}/tx` and returns the response body trimmed as the txid. Non-REST keeps `sendrawtransaction`.

- [ ] **Step 1: Write the failing sign test**

Use `btcec.NewPrivateKey()`, derive the P2WPKH address with `addressing.DeriveBtcAddress("bc", priv.PubKey().SerializeCompressed())`. Build a one-input one-output unsigned metadata worth 100000 sats in and 90000 sats out. Sign. Deserialize. Assert `len(msg.TxIn[0].Witness) == 2`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/chain/ -count=1 -run TestBitcoinSignP2WPKH`

Expected: FAIL, `BTC signing not implemented`

- [ ] **Step 3: Implement sign**

```go
func signBitcoinP2WPKH(unsigned *types.UnsignedTx, privateKey []byte, net *chaincfg.Params) (*types.SignedTx, error) {
	if len(privateKey) != 32 {
		return nil, fmt.Errorf("btc private key must be 32 bytes")
	}
	priv := btcec.PrivKeyFromBytes(privateKey)
	msg, prevFetcher, err := unsignedToMsgTx(unsigned)
	if err != nil {
		return nil, err
	}
	sigHashes := txscript.NewTxSigHashes(msg, prevFetcher)
	for i, in := range inputsFrom(unsigned) {
		pkScript, err := txscript.PayToAddrScript(mustDecodeAddress(in.Address, net))
		if err != nil {
			return nil, err
		}
		witness, err := txscript.WitnessSignature(msg, sigHashes, i, in.Value, pkScript, txscript.SigHashAll, priv, true)
		if err != nil {
			return nil, err
		}
		msg.TxIn[i].Witness = witness
	}
	var buf bytes.Buffer
	if err := msg.Serialize(&buf); err != nil {
		return nil, err
	}
	return &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: buf.Bytes(), TxHash: msg.TxHash().String()}, nil
}
```

Metadata schema, stored as `[]btcInput` and `[]btcOutput` under `inputs` and `outputs` (not loose maps):

```go
type btcInput struct {
	TxID    string // hex, no 0x
	Vout    uint32
	Value   int64
	Address string
}
type btcOutput struct {
	Address string
	Value   int64
}

func inputsFrom(unsigned *types.UnsignedTx) []btcInput {
	raw, _ := unsigned.Metadata["inputs"].([]btcInput)
	return raw
}

func unsignedToMsgTx(unsigned *types.UnsignedTx) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	msg := wire.NewMsgTx(wire.TxVersion)
	prev := make(map[wire.OutPoint]*wire.TxOut)
	for _, in := range inputsFrom(unsigned) {
		hash, err := chainhash.NewHashFromStr(in.TxID)
		if err != nil {
			return nil, nil, err
		}
		op := wire.OutPoint{Hash: *hash, Index: in.Vout}
		msg.AddTxIn(wire.NewTxIn(&op, nil, nil))
		addr, err := btcutil.DecodeAddress(in.Address, netParams(unsigned))
		if err != nil {
			return nil, nil, err
		}
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, nil, err
		}
		prev[op] = wire.NewTxOut(in.Value, pkScript)
	}
	for _, out := range unsigned.Metadata["outputs"].([]btcOutput) {
		addr, err := btcutil.DecodeAddress(out.Address, netParams(unsigned))
		if err != nil {
			return nil, nil, err
		}
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, nil, err
		}
		msg.AddTxOut(wire.NewTxOut(out.Value, pkScript))
	}
	return msg, txscript.NewMultiPrevOutFetcher(prev), nil
}
```

`netParams` returns `chaincfg.TestNet3Params` when `unsigned.Metadata["testnet"] == true`, otherwise `chaincfg.MainNetParams`. `mustDecodeAddress` is `btcutil.DecodeAddress` with that same net. `signBitcoinP2WPKH` passes `netParams(unsigned)` into `PayToAddrScript`.

- [ ] **Step 4: Run sign test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run TestBitcoinSignP2WPKH`

Expected: PASS

- [ ] **Step 5: Write the failing UTXO and broadcast tests**

`httptest` serves `/address/bc1qexample/utxo` with one confirmed UTXO of 200000 sats. `BuildTransfer` of 100000 sats to a second bc1 address yields metadata whose output values sum with the fee to 200000.

Broadcast test: `restAPI` true, `POST /tx` receives the hex body, response body is `abc123\n`. `BroadcastTransaction` returns `abc123`.

- [ ] **Step 6: Implement UTXO selection, BuildSweep, REST broadcast**

Selection and fee rule are the “Done when” paragraph of this task. `BuildSweep` returns `[]types.UnsignedTx{*built}` or `ErrUnsupportedChain` must no longer be returned.

REST broadcast:

```go
if a.restAPI {
	url := strings.TrimRight(a.cfg.RPCURL, "/") + "/tx"
	bodyHex := hex.EncodeToString(signed.RawBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(bodyHex))
	if err != nil {
		return "", err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("btc broadcast %d: %s", resp.StatusCode, body)
	}
	return strings.TrimSpace(string(body)), nil
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./app/services/chain/ -count=1 -run 'TestBitcoin'`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add app/services/chain/bitcoin.go app/services/chain/bitcoin_tx.go app/services/chain/bitcoin_tx_test.go
git commit -m "feat: sign Bitcoin P2WPKH withdrawals and broadcast over REST"
```

### Task 8: Reconstruct secp256k1 and sign SOL/BTC in the executor

**Files:**
- Modify: `app/services/mpc/service.go`
- Modify: `app/services/mpc/signing.go`
- Modify: `tests/mocks/mpc.go`
- Modify: `app/services/sweep/executor.go` (`broadcastLeg` sign block and `broadcastWithdrawal` sign block)
- Test: `app/services/mpc/signing_test.go` (create if the package has no test file for this function; `service_test.go` already keygens ed25519)
- Test: `app/services/sweep/executor_test.go`

EVM chain ids `models.ChainETH`, `models.ChainPolygon`, `models.ChainTETH`, and `models.ChainTPolygon` keep `mpc.Sign` plus `finalizeMPCTransaction`. Chain ids `models.ChainSOL`, `models.ChainTSOL`, `models.ChainBTC`, and `models.ChainTBTC` reconstruct a private key and call `adapter.SignTransaction`. Existing executor tests use `models.ChainETH` and must still call `mpc.Sign`.

- [ ] **Step 1: Write the failing reconstruct test**

Keygen two secp256k1 shares with `TSSService.Keygen(ctx, CurveSecp256k1)`. Reconstruct. `btcec.PrivKeyFromBytes` public key compressed bytes must equal `KeygenResult.CombinedPubKey`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/mpc/ -count=1 -run TestReconstructSecp256k1`

Expected: FAIL, method missing

- [ ] **Step 3: Implement reconstruct**

Add to the `Service` interface:

```go
ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error)
```

Implement beside `ReconstructEd25519PrivateKey`, using `ecdsa/keygen.LocalPartySaveData` and `btcec.S256().N`:

```go
scalar := new(big.Int).Mod(new(big.Int).Add(saveA.Xi, saveB.Xi), btcec.S256().N)
out := make([]byte, 32)
b := scalar.Bytes()
copy(out[32-len(b):], b)
return out, nil
```

Reject nil `Xi`. Mock:

```go
func (m *MockMPCService) ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error) {
	if m.ReconstructSecp256k1Fn != nil {
		return m.ReconstructSecp256k1Fn(shareA, shareB)
	}
	return bytes.Repeat([]byte{0x11}, 32), nil
}
```

- [ ] **Step 4: Write the failing executor test**

Add a plan with `Chain: models.ChainSOL` and a mock chain whose `SignTransactionFn` records the private key length. MPC mock `ReconstructEd25519PrivateKey` returns 32 bytes `0x22`. Assert `SignTransaction` was called once and `Sign` was not called.

Add the eth regression: existing direct-from-base test still increments `Sign` and does not call `SignTransaction`.

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./app/services/sweep/ -count=1 -run 'TestExecutePlan_SolanaSignsLocally|TestExecutePlan_EVM'`

Expected: FAIL, SOL path still calls `mpc.Sign` and that mock or the real guard returns `unsupported curve`

- [ ] **Step 6: Implement the branch**

```go
func localSignChain(chainID string) bool {
	switch chainID {
	case models.ChainSOL, models.ChainTSOL, models.ChainBTC, models.ChainTBTC:
		return true
	default:
		return false
	}
}

func (s *service) signUnsigned(ctx context.Context, adapter types.Chain, curve mpcpkg.Curve, shareA, shareB []byte, wallet *models.Wallet, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	if !localSignChain(unsigned.ChainID) {
		sig, err := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{TxHashes: [][]byte{unsigned.RawBytes}})
		if err != nil {
			return nil, err
		}
		return finalizeMPCTransaction(adapter, unsigned, sig, wallet)
	}
	var seed []byte
	var err error
	switch curve {
	case mpcpkg.CurveEd25519:
		seed, err = s.mpc.ReconstructEd25519PrivateKey(shareA, shareB)
	case mpcpkg.CurveSecp256k1:
		seed, err = s.mpc.ReconstructSecp256k1PrivateKey(shareA, shareB)
	default:
		err = fmt.Errorf("unsupported curve %s", curve)
	}
	if err != nil {
		return nil, err
	}
	defer zeroBytes(seed)
	return adapter.SignTransaction(ctx, unsigned, seed)
}
```

`unsigned.ChainID` must be set by SOL and BTC `BuildTransfer` to `a.cfg.ChainIDStr`. EVM already sets it. Replace the sign blocks in `broadcastLeg` and `broadcastWithdrawal` with `signUnsigned`. Persist `tx.Asset` from `plan.Asset` on the three transaction structs in this file (sweep leg, and both withdrawal structs). Polygon native stays `models.NativePOL` on the adapter and on the row.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./app/services/mpc/ ./app/services/sweep/ -count=1 -run 'TestReconstructSecp256k1|TestExecutePlan'`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add app/services/mpc/service.go app/services/mpc/signing.go app/services/mpc/signing_test.go \
  tests/mocks/mpc.go app/services/sweep/executor.go app/services/sweep/executor_test.go
git commit -m "feat: sign SOL and BTC withdrawals from reconstructed keys"
```

### Task 9: Open sweep planning for SOL and BTC

**Files:**
- Modify: `app/services/sweep/planner.go` (the `AdapterType != evm` return, and `chainNeedsGasSeed`)
- Modify: `app/services/sweep/consolidate.go` (the same adapter gate)
- Modify: `app/services/withdraw/service.go` (the `StrategyMultiSweep` gas-status check)
- Modify: `app/services/sweep/planner_test.go` (the test that expects `ErrUnsupportedChain` for a SOL wallet)
- Modify: `app/services/sweep/consolidate_test.go` (the BTC test that expects `ErrUnsupportedChain` before any lock)

**Done when:** a SOL or BTC wallet with a base balance covering `amount` returns `StrategyDirectFromBase`. `chainNeedsGasSeed` returns false for `models.ChainSOL`, `models.ChainTSOL`, `models.ChainBTC`, and `models.ChainTBTC`, and true for EVM chain ids. `ConsolidateAll` passes the adapter gate for those chains and still returns `ErrUnsupportedChain` for an unknown adapter type such as `""`. `withdraw.Service.Request` returns `ErrWalletNotGasReady` for EVM `multi_sweep` when gas status is not seeded, and does not return that error for SOL or BTC `multi_sweep`.

- [ ] **Step 1: Replace the SOL planner expectation**

In `planner_test.go`, the test around the SOL wallet that expects `ErrUnsupportedChain` becomes: base balance equal to `amount`, expect `StrategyDirectFromBase` and nil error. Add a bitcoin case with the same expectation. Keep an EVM test that still plans.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./app/services/sweep/ -count=1 -run 'TestPlanForWithdrawal_Solana|TestPlanForWithdrawal_Bitcoin'`

Expected: FAIL, `ErrUnsupportedChain`

- [ ] **Step 3: Open the gate**

Delete the block that returns `ErrUnsupportedChain` when `AdapterType != evm` in `PlanForWithdrawal`. Allow `models.AdapterTypeEVM`, `models.AdapterTypeSolana`, and `models.AdapterTypeBitcoin`. Any other adapter type still returns `ErrUnsupportedChain`.

```go
func chainNeedsGasSeed(chainID string) bool {
	switch chainID {
	case models.ChainSOL, models.ChainTSOL, models.ChainBTC, models.ChainTBTC:
		return false
	default:
		return true
	}
}
```

Apply the same allow-list in `ConsolidateAll` before the lock, so a BTC wallet no longer fails before `acquireWalletOpsLock`. The old consolidate test must be rewritten to get past the gate and then fail on the next real guard (missing passphrase is already checked; use a 12+ char passphrase and a mock that has no children and expect a successful empty result or the existing lock path). Do not leave a test that requires `ErrUnsupportedChain` for BTC.

In `withdraw.Service.Request`, wrap the gas-status check:

```go
case sweep.StrategyMultiSweep:
	adapter, adapterErr := s.registry.Chain(wallet.Chain)
	if adapterErr != nil {
		return nil, nil, adapterErr
	}
	if adapter.GasReadinessThreshold() != nil && wallet.GasStatus != models.GasStatusSeeded {
		return nil, nil, sweep.ErrWalletNotGasReady
	}
```

`SolanaLive.GasReadinessThreshold` and `BitcoinLive.GasReadinessThreshold` already return nil. EVM returns a non-nil threshold from config.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./app/services/sweep/ ./app/services/withdraw/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/services/sweep/planner.go app/services/sweep/planner_test.go \
  app/services/sweep/consolidate.go app/services/sweep/consolidate_test.go \
  app/services/withdraw/service.go app/services/withdraw/service_test.go
git commit -m "feat: plan SOL and BTC withdrawals without an EVM gas seed"
```

### Task 10: Track Wallets verification

- [ ] **Step 1: Run the focused packages**

Run from `macro-wallets/back`:

```bash
go test ./app/services/chain/ ./app/services/ingest/... ./app/services/deposit/ \
  ./app/services/withdraw/ ./app/services/sweep/ ./app/services/mpc/ -count=1
```

Expected: PASS

- [ ] **Step 2: Run the full suite**

Run: `make test`

Expected: exit 0. This needs the test database (`TEST_DB_REQUIRED=1` in the Makefile). If Docker is down, start it with `make docker-up` and rerun. Do not skip the suite and claim Track Wallets done.

- [ ] **Step 3: Commit only if Step 1 or Step 2 forced a fix**

A green run with no diff has nothing to commit.

Track Markets (Section B) is already in progress in parallel. Do not treat this verification as a gate that Section B waits on.

---

## Section B — Track Markets (Macro Markets)

Starts now, in parallel with Track Wallets. Repo root: `/home/raphaelcangucu/macro-markets/back`. Tests are Pest (`php artisan test` on the files named in each task). Pest files that name a provider, chain id, or ledger code import `App\Models\Currency` and use those constants. JSON webhook fixtures stay string literals.

### Task 11: Feature flags and active provider

**Files:**
- Modify: `app/Models/Currency.php`
- Create: `app/Features/CryptoCustodyBitGoFeature.php`
- Create: `app/Features/CryptoCustodyMacroWalletsFeature.php`
- Create: `app/Services/CryptoCustody/ActiveProvider.php`
- Test: `tests/Unit/CryptoCustody/ActiveProviderTest.php`

Add these constants on `App\Models\Currency` before the tests below. Later Section B tasks use them. Do not add a new class. Do not also declare them on `BitgoWallet`. `ETH`, `POL`, `BTC`, `SOL`, `USDT`, and `USDC` already exist on `Currency` and stay the ledger codes.

```php
public const PROVIDER_BITGO = 'bitgo';

public const PROVIDER_MACRO_WALLETS = 'macro_wallets';

public const CHAIN_ETH = 'eth';

public const CHAIN_POLYGON = 'polygon';

public const CHAIN_BTC = 'btc';

public const CHAIN_SOL = 'sol';
```

`ActiveProvider::name()` returns `CryptoCustodySettings.active_provider` (`Currency::PROVIDER_BITGO` or `Currency::PROVIDER_MACRO_WALLETS`) only when that provider's Pennant flag is on. Both flags may be on. No selected provider, an unknown value, or a selected provider whose flag is off throws `App\Exceptions\CryptoCustodyProviderConflictException` with message `crypto custody provider is not active`. `ActiveProvider::select(string $provider)` writes `active_provider` and does not deactivate the other flag.

Follow the empty feature class shape of `app/Features/MarketsFeature.php`. Register both classes the same way that file is discovered by Pennant (class name is the feature name).

- [ ] **Step 1: Write the failing test**

```php
it('allows both flags and returns the selected provider', function () {
    Feature::for(null)->activate(CryptoCustodyBitGoFeature::class);
    Feature::for(null)->activate(CryptoCustodyMacroWalletsFeature::class);
    app(ActiveProvider::class)->select(Currency::PROVIDER_MACRO_WALLETS);
    expect(app(ActiveProvider::class)->name())->toBe(Currency::PROVIDER_MACRO_WALLETS);
    expect(Feature::for(null)->active(CryptoCustodyBitGoFeature::class))->toBeTrue();
});

it('rejects a selected provider whose flag is off', function () {
    Feature::for(null)->deactivate(CryptoCustodyMacroWalletsFeature::class);
    app(ActiveProvider::class)->select(Currency::PROVIDER_MACRO_WALLETS);
    expect(fn () => app(ActiveProvider::class)->name())
        ->toThrow(CryptoCustodyProviderConflictException::class);
});
```
- [ ] **Step 2: Run `php artisan test tests/Unit/CryptoCustody/ActiveProviderTest.php` and see the exception missing**
- [ ] **Step 3: Implement the two feature classes and `ActiveProvider`**
- [ ] **Step 4: Re-run the test and see PASS**
- [ ] **Step 5: Commit `feat: select the active crypto custody provider`**

### Task 12: WalletProviderFactory

**Files:**
- Create: `app/Interfaces/WalletCustodyProvider.php` with `createAddress`, `withdraw`, `providerName`
- Create: `app/Services/CryptoCustody/BitGoCustodyProvider.php` — delegates address creation and withdraw to the existing `App\Services\Payment\BitGoService` methods those flows already call
- Create: `app/Services/CryptoCustody/MacroWalletsCustodyProvider.php` — HTTP client to the Macro Wallets base URL from settings, `POST /api/v1/wallets/{id}/withdrawals` with `asset`, and the existing address-creation endpoint the BitGo path will be swapped off of
- Create: `app/Factories/WalletProviderFactory.php`
- Test: `tests/Unit/CryptoCustody/WalletProviderFactoryTest.php`

`create()` takes no argument. It calls `ActiveProvider::name()` and matches `Currency::PROVIDER_BITGO` or `Currency::PROVIDER_MACRO_WALLETS`. Any other value throws `InvalidArgumentException`. Mirror `app/Factories/PixGatewayFactory.php`.

Before this task, read `BitGoService` and name the real methods in the delegating provider. Do not add a second BitGo HTTP client.

- [ ] **Step 1: Write the failing test**

```php
it('resolves the active custody provider', function (string $provider, string $class) {
    app(ActiveProvider::class)->select($provider);
    expect(app(WalletProviderFactory::class)->create())->toBeInstanceOf($class);
})->with([
    [Currency::PROVIDER_BITGO, BitGoCustodyProvider::class],
    [Currency::PROVIDER_MACRO_WALLETS, MacroWalletsCustodyProvider::class],
]);
```
- [ ] **Step 2: Run the test, expect class missing**
- [ ] **Step 3: Implement factory and both providers**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: add WalletProviderFactory`**

### Task 13: Settings page `settings/crypto-custody`

**Files:**
- Create: `app/Settings/CryptoCustodySettings.php` (Spatie settings group `cryptoCustody`)
- Create: `app/Filament/Pages/ManageCryptoCustody.php`
- Modify: `config/settings.php` — register `CryptoCustodySettings`, remove `BitGoSettings` from the list only after the migration copies values
- Create: a migration that copies `bitGo` settings into `cryptoCustody` and adds the new keys
- Delete the navigation entry by removing `app/Filament/Pages/ManageBitGo.php` from the Filament discovery path (delete the file once the new page renders the same BitGo fields)
- Modify: `database/seeders/BitGoSettingsMenuSeeder.php` so the menu slug is `settings/crypto-custody`
- Test: `tests/Unit/CryptoCustody/CryptoCustodySettingsPageTest.php`

Fields moved from `app/Settings/BitGoSettings.php`: `min_consolidate_value`, `express_url`, `enterprise`, `access_token`, `wallet_default_passphrase`, `web_hook_url`, `web_hook_id`, `wallet_label`, `address_creation_attempts`. Add `bitgo_environment` and `macro_wallets_environment`, each enum `testnet` or `prod`. Add `macro_wallets_base_url`, `macro_wallets_api_token`, `macro_wallets_webhook_secret`.

The page shows the fields of the provider selected in `active_provider`. `Test BitGo` HTTP GETs `{express_url}/user/session` with the access token and flashes the status code. `Test Macro Wallets` HTTP GETs `{macro_wallets_base_url}/api/v1/chains` with `Authorization: Bearer {macro_wallets_api_token}` and flashes the status code. A connection exception flashes the exception message and does not save.

`ActiveProvider::select` runs when the operator saves `active_provider`. It does not turn the other feature flag off.

- [ ] **Step 1: Write the failing test**

```php
it('exposes crypto custody settings and retires the BitGo page', function () {
    expect(ManageCryptoCustody::getSlug())->toBe('settings/crypto-custody');
    expect(class_exists(ManageBitGo::class))->toBeFalse();
});
```
- [ ] **Step 2: Run it, expect the old slug**
- [ ] **Step 3: Implement settings, migration, page, and delete `ManageBitGo.php`**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: replace BitGo settings with crypto custody settings`**

### Task 14: Rename `bitgo_wallets`

**Files:**
- Create: `database/migrations/2026_10_06_000001_rename_bitgo_wallets_to_crypto_custody_wallets.php`
- Modify: `app/Models/BitgoWallet.php` — `protected $table = 'crypto_custody_wallets'`. Add `provider` to `$fillable`. Keep the class name for this task so call sites compile. Do not add provider constants here. Use `Currency::PROVIDER_BITGO` and `Currency::PROVIDER_MACRO_WALLETS` from Task 11.
- Modify: `app/Filament/Resources/BitgoWalletsResource.php` — hide `consolidate_wallet` and `consolidate_wallet_batch` unless `provider === Currency::PROVIDER_BITGO`
- Test: `tests/Unit/CryptoCustody/CryptoCustodyWalletsMigrationTest.php`

Migration `up`:

```php
Schema::rename('bitgo_wallets', 'crypto_custody_wallets');
Schema::table('crypto_custody_wallets', function (Blueprint $table) {
    $table->string('provider')->default(Currency::PROVIDER_BITGO);
});
DB::table('crypto_custody_wallets')->whereNull('provider')->update(['provider' => Currency::PROVIDER_BITGO]);
```

`down` drops `provider` and renames the table back. Foreign keys that reference `bitgo_wallets.id` (`wallet_bitgo_wallet` from `database/migrations/2023_07_17_105024_wallet_bitgo_wallet.php`) must be dropped and recreated against `crypto_custody_wallets` inside this migration. Read the current constraint name from the database before writing the drop, and put that name in the migration. Do not leave the constraint pointing at the old table.

For `provider = Currency::PROVIDER_MACRO_WALLETS`, the create form does not offer `type` deposit/withdraw. It sets `type` to `custody` and the factory refuses a second row with the same `provider` and chain. Chain is the currency’s network, not a new column, unless the currency model has no network: then add `chain` on `crypto_custody_wallets` and set it from `Currency::ETH` → `Currency::CHAIN_ETH`, `Currency::POL` → `Currency::CHAIN_POLYGON`, `Currency::BTC` → `Currency::CHAIN_BTC`, `Currency::SOL` → `Currency::CHAIN_SOL`, and `Currency::USDT` / `Currency::USDC` plus the selected network (`Currency::CHAIN_ETH`, `Currency::CHAIN_POLYGON`, or `Currency::CHAIN_SOL`). One row per `(provider, chain)` for `Currency::PROVIDER_MACRO_WALLETS`. BitGo rows are not collapsed. There is no MATIC alias.

- [ ] **Step 1: Write the failing test**

```php
it('renames bitgo wallets and backfills provider', function () {
    expect(Schema::hasTable('crypto_custody_wallets'))->toBeTrue();
    expect(Schema::hasColumn('crypto_custody_wallets', 'provider'))->toBeTrue();
    $row = DB::table('crypto_custody_wallets')->first();
    expect($row->provider)->toBe(Currency::PROVIDER_BITGO);
});
```
- [ ] **Step 2: Run it, expect the old table**
- [ ] **Step 3: Migration, model table, hide the two consolidate actions**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: rename bitgo_wallets to crypto_custody_wallets`**

### Task 15: `wallet_addresses.provider`

**Files:**
- Create: `database/migrations/2026_10_06_000002_add_provider_to_wallet_addresses.php`
- Modify: `app/Models/WalletAddress.php` — `provider` fillable, not on any Filament edit form
- Find the insert site in `app/Services/Payment/BitGo/BitGoWalletAddress.php` and the new Macro Wallets address creator. Set `provider` there. Do not add a mutator that allows updates.
- Test: `tests/Unit/CryptoCustody/WalletAddressProviderTest.php`

```php
$table->string('provider')->default(Currency::PROVIDER_BITGO);
```

Backfill is the default. The test updates a row’s `provider` through `$address->update(['provider' => Currency::PROVIDER_MACRO_WALLETS])` after create and expects the stored value to stay the insert value. Enforce that with a model `updating` hook that unsets `provider` when the row already exists.

- [ ] **Step 1: Write the failing test**

```php
it('keeps the provider assigned at insert', function () {
    $address = WalletAddress::factory()->create(['provider' => Currency::PROVIDER_BITGO]);
    $address->update(['provider' => Currency::PROVIDER_MACRO_WALLETS]);
    expect($address->fresh()->provider)->toBe(Currency::PROVIDER_BITGO);
});
```
- [ ] **Step 2: Run, expect column missing**
- [ ] **Step 3: Migration and model hook**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: store custody provider on wallet addresses`**

### Task 16: Dual webhooks, credit only on provider match

**Files:**
- Modify: `app/Http/Controllers/BitGoWebhookController.php`
- Create: `app/Http/Controllers/MacroWalletsWebhookController.php`
- Modify: the route file that registers `bitgo/webhook` (search `BitGoWebhookController`). Add the Macro Wallets route next to it. Do not remove the BitGo route.
- Test: `tests/Unit/Wallet/CustodyWebhookProviderMatchTest.php`

BitGo path: after the transfer is loaded, resolve the deposit address. If `wallet_addresses.provider !== Currency::PROVIDER_BITGO`, return 200 `{message: ignored}` and do not dispatch `ProcessBitGoTransferJob`.

Macro Wallets path: verify `macro_wallets_webhook_secret`, resolve the address, ignore unless `provider === Currency::PROVIDER_MACRO_WALLETS`. On match, credit the user ledger with `Transaction.Asset` from the payload. `Currency::POL` credits the POL currency. Do not credit when the address is missing. USDT and USDC credits use `Currency::USDT` and `Currency::USDC`.

- [ ] **Step 1: Write the failing test**

```php
it('ignores a bitgo webhook for a macro wallets address', function () {
    $address = WalletAddress::factory()->create(['provider' => Currency::PROVIDER_MACRO_WALLETS]);
    $this->post('/api/bitgo/webhook', bitgoTransferPayload($address->address))
        ->assertOk()
        ->assertJson(['message' => 'ignored']);
    expect(WalletTransaction::query()->count())->toBe(0);
});
```

`bitgoTransferPayload` is the array already built in `tests/Unit/Wallet/ConfirmPendingDepositsJobTest.php`. Point it at `$address->address`. That helper is an external BitGo webhook body, so coin and asset strings inside it stay literals. The matching-provider case posts the same payload for `provider = Currency::PROVIDER_BITGO` and expects one ledger row.
- [ ] **Step 2: Run, expect a ledger row for the mismatch**
- [ ] **Step 3: Implement both guards**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: credit crypto webhooks only for the address provider`**

### Task 17: Wire new addresses and new withdrawals through the factory

**Files:**
- Modify the deposit address job (`app/Jobs/BitGoWalletCreateJob.php` and `BitGoWalletAddress`) so new addresses call `WalletProviderFactory` instead of BitGo directly
- Modify `app/Services/Payment/BitGoWithdrawService.php` call sites used by `app/GraphQL/Mutations/Withdraw.php` so new crypto withdrawals call the factory
- Test: extend `tests/Unit/CryptoCustody/WalletProviderFactoryTest.php` with a feature-flag switch: active `macro_wallets` does not call BitGo Express

No internal transfer job is added. Do not create a withdraw-type row for `Currency::PROVIDER_MACRO_WALLETS`.

- [ ] **Step 1: Write the failing test**

```php
it('does not call bitgo when macro wallets is the active provider', function () {
    Http::fake();
    app(ActiveProvider::class)->select(Currency::PROVIDER_MACRO_WALLETS);
    app(WalletProviderFactory::class)->create()->createAddress($currency, $user);
    Http::assertNotSent(fn ($request) => str_contains(
        $request->url(),
        app(CryptoCustodySettings::class)->express_url
    ));
    expect(WalletAddress::query()->where('provider', Currency::PROVIDER_MACRO_WALLETS)->exists())->toBeTrue();
});
```

`createAddress(Currency $currency, User $user)` is the method on `WalletCustodyProvider`. Seed `$currency` and `$user` with the factories those BitGo address tests already use.
- [ ] **Step 2: Run, expect a BitGo HTTP call**
- [ ] **Step 3: Swap the two call sites to the factory**
- [ ] **Step 4: PASS**
- [ ] **Step 5: Commit `feat: route new crypto addresses and withdrawals through the active provider`**

---

## Section C — Other wallets (not scheduled)

No tasks. No files. These are not in the essential slice:

- Chains with no adapter here: LTC, XRP, DOGE, DOT, ADA, BCH, AVAX, TON, BNB, TRX.
- SHIB needs a new `tokens` row on `eth` before it can be scanned. Not added in this effort.
- DAI and LINK already have EVM rows in `database/seeds/tokens.go` and stay out by product decision.
- `BSC:USDT`, `BSC:USDC`, and `TRX:USDT` stay out. There is no BSC or TRX chain.

---

## Section D — Cross-repo integration and e2e

Tests only. Product code stays as Tasks 1–17 left it. A red scenario here is a contract break to fix in the owning track before either track is called done.

Section D runs after Tasks 1–17 are green. It does not renumber them. The Macro Wallets scenario and the BitGo scenario are separate files and separate Pest cases. `Currency::PROVIDER_BITGO` and `Currency::PROVIDER_MACRO_WALLETS` are the only provider values. Chain and asset assertions use `Currency::CHAIN_POLYGON`, `Currency::USDT`, `Currency::POL` on the Markets side and `models.ChainPolygon`, `models.SymbolUSDT`, `models.NativePOL` on the Wallets side. There is no MATIC alias. Both webhook routes stay registered.

### Harness

Macro Wallets has no helper that listens on a port for another process. Controller tests dispatch inside the Goravel process through `app/http/controllers/testutil/request.go` (`s.Http(t)`). `tests/testutil.BootTest()` loads `APP_ENV=testing` and database `vault_test`. `httptest.NewServer` in `app/services/webhook/service_test.go` stands in for an outbound receiver. The HTTP server other processes can call is the one local already uses: `go run .` → `runLocal` → `facades.Route().Run` on `PORT` (`config/vault.go`, default `8080`; dev `.env` uses `2002`). Readiness is `GET /health` (`routes/docs.go`).

Macro Markets boots tests with Pest and `Tests\TestCase` (`tests/Pest.php`, `phpunit.xml` sets `APP_ENV=testing`). Feature tests already hit the Laravel kernel (`tests/Feature/Filament/FilamentAdminSmokeTest.php`). Filament behavior uses `Livewire::test` (`tests/Unit/Filament/Pages/BitgoWalletTransferTest.php`).

The integration harness is that Pest file calling the booted Goravel process. Start Docker from `macro-wallets/back` with `make docker-up` (`waas-postgres`, `waas-redis`, `waas-localstack`). Boot Wallets with `PORT=2002 APP_ENV=testing DB_DATABASE=vault_test go run .` and wait until `GET http://127.0.0.1:2002/health` returns 200. Point `CryptoCustodySettings.macro_wallets_base_url` at `http://127.0.0.1:2002`. The Pest file must leave that host unfaked.

The callback needs a Markets process that listens. Pest’s `$this->post` stays inside the test kernel, so it cannot receive Wallets’ HTTP client. Boot the same Laravel app with `php artisan serve --host=127.0.0.1 --port=8001` on the testing database. `Tests\TestCase` does not use `RefreshDatabase` or `DatabaseTransactions`, so a committed `wallet_addresses` row is visible to that server. `webhook.Service.Deliver` (`app/services/webhook/service.go`) is the caller: it POSTs the raw body and sets `X-Vault-Signature` to hex HMAC-SHA256 of that body. `EnqueueEvent` wraps the transaction model as `data` with `type` `deposit.confirmed` (`types.EventDepositConfirmed`). The transaction JSON includes `to_address` and `asset`. Drive `Deliver` from `deliver_markets_integration_test.go`. That Go test skips when `MARKETS_WEBHOOK_URL` is empty, so `make test` does not require Markets. The Pest file exports `MARKETS_WEBHOOK_URL=http://127.0.0.1:8001/api/macro-wallets/webhook` and the shared secret, runs the Go test, and fails if that test skips or exits non-zero.

User-facing deposit address creation is GraphQL `depositAddress` (`app/GraphQL/Mutations/Deposit.php`), already covered in-process by `tests/Unit/Graphql/Finance/DepositAddressTest.php`. `macro-markets/front/e2e/` is checkout PIX (`e2e/first-position-checkout.spec.ts` and the evidence and video specs). `macro-wallets/front/e2e/create-wallet-real.spec.ts` and `e2e/deposit-withdraw.spec.ts` cover the WaaS dashboard. Section D does not add a Playwright project. E2E is the Pest feature flow through those controllers and through Filament Livewire.

### Task 18: Integration — Markets calls Wallets, Wallets calls back

**Files:**
- Create: `macro-markets/back/tests/Feature/CryptoCustody/MacroWalletsHttpContractTest.php` beside `tests/Unit/CryptoCustody/WalletProviderFactoryTest.php` and `tests/Unit/Wallet/CustodyWebhookProviderMatchTest.php`
- Create: `macro-wallets/back/app/services/webhook/deliver_markets_integration_test.go` beside `app/services/webhook/service_test.go`

Sits on the real `MacroWalletsCustodyProvider` client. `Http::fake()` must not match `macro_wallets_base_url`.

- [ ] **Step 1: Boot Wallets and Markets HTTP**

From `macro-wallets/back`: `make docker-up`, migrate `vault_test`, then `PORT=2002 APP_ENV=testing DB_DATABASE=vault_test go run .`. From `macro-markets/back`: `php artisan serve --host=127.0.0.1 --port=8001` with `APP_ENV=testing`.

- [ ] **Step 2: Write the Pest scenario**

Select `Currency::PROVIDER_MACRO_WALLETS` with both Pennant flags on. Store a custody wallet row whose id in the URL is a real Wallets wallet on chain `models.ChainPolygon` (`Currency::CHAIN_POLYGON`). Mint a Wallets API token and save it as `macro_wallets_api_token`. Set `macro_wallets_webhook_secret` to the same secret registered on that wallet’s webhook config.

Assertions:

- `POST /api/v1/wallets/{id}/addresses` with `Authorization: Bearer {token}` returns 201 and a non-empty `address`. Markets stores that string on `wallet_addresses` with `provider = Currency::PROVIDER_MACRO_WALLETS`.
- The same POST with a wrong bearer returns 401 and stores no address.
- `POST /api/v1/wallets/{id}/withdrawals` sends `asset` = `Currency::USDT`. Wallets accepts that value as `models.SymbolUSDT` (validation does not 422 it). An asset outside the slice (SHIB, the same literal Task 1 uses) returns 422.
- The Go test calls `webhook.Service.Deliver` with `type` `deposit.confirmed`, `data.to_address` equal to the address from the POST, and `data.asset` = `models.SymbolUSDT`. The header is `X-Vault-Signature`. Markets returns 200 and credits one ledger row in `Currency::USDT` only when `wallet_addresses.provider` is `Currency::PROVIDER_MACRO_WALLETS`.
- The same signed body pointed at an address stored as `Currency::PROVIDER_BITGO` returns 200 `{message: ignored}` and adds no ledger row.
- A body with a bad `X-Vault-Signature` returns 401 and adds no ledger row.

Commit the address row before `Deliver` runs.

- [ ] **Step 3: Run**

```bash
php artisan test tests/Feature/CryptoCustody/MacroWalletsHttpContractTest.php
```

Expected: exit 0. The child `go test ./app/services/webhook/ -count=1 -run TestIntegrationDeliverDepositConfirmed` exits 0 and does not skip. `make test` may skip that Go test when `MARKETS_WEBHOOK_URL` is empty; that skip does not close this task.

- [ ] **Step 4: Commit**

```bash
git add tests/Feature/CryptoCustody/MacroWalletsHttpContractTest.php
git commit -m "test: call Macro Wallets over HTTP for address, asset, and deposit webhook"
```

The Go file commits in `macro-wallets/back`:

```bash
git add app/services/webhook/deliver_markets_integration_test.go
git commit -m "test: deliver deposit.confirmed to the Markets webhook URL"
```

### Task 19: E2E admin — Macro Wallets settings, connection, custody row, user address

**Files:**
- Create: `macro-markets/back/tests/Feature/Filament/ManageCryptoCustodyE2ETest.php` beside `tests/Feature/Filament/FilamentAdminSmokeTest.php` and `tests/Unit/CryptoCustody/CryptoCustodySettingsPageTest.php`
- The user-address half of this scenario may call the same GraphQL entry as Task 20; keep the admin setup in this file.

Actor is a Filament superadmin, same setup as `FilamentAdminSmokeTest`. Drive the page with `Livewire::test(ManageCryptoCustody::class)` and the custody-wallet create page with `Livewire::test(CreateBitgoWallet::class)` (`app/Filament/Resources/BitgoWalletsResource/Pages/CreateBitgoWallet.php`). Reuse the Goravel process from Task 18 (`GET http://127.0.0.1:2002/health` is 200). This task does not `Http::fake()` `macro_wallets_base_url`.

- [ ] **Step 1: Write the scenario**

- Save `active_provider` = `Currency::PROVIDER_MACRO_WALLETS` through the settings page. `ActiveProvider::name()` returns that constant. Both feature flags stay on.
- Saved settings include `macro_wallets_base_url`, `macro_wallets_api_token`, and `macro_wallets_webhook_secret`.
- Action `test_macro_wallets` GETs `{macro_wallets_base_url}/api/v1/chains` with `Authorization: Bearer {macro_wallets_api_token}`. The notification title is `200` (`flashStatus`). A refused token or a down port fails this test.
- Create one `crypto_custody_wallets` row through `CreateBitgoWallet` with `provider` = `Currency::PROVIDER_MACRO_WALLETS`, `type` = `custody`, and chain `Currency::CHAIN_POLYGON`. A second row for the same provider and chain is refused.
- With that row in place, GraphQL `depositAddress` for a user (the query in `tests/Unit/Graphql/Finance/DepositAddressTest.php`) returns the address Wallets issued, and `wallet_addresses.provider` is `Currency::PROVIDER_MACRO_WALLETS`.

- [ ] **Step 2: Run `php artisan test tests/Feature/Filament/ManageCryptoCustodyE2ETest.php`**

Expected: exit 0.

- [ ] **Step 3: Commit `test: cover crypto custody admin setup against Macro Wallets`**

### Task 20: E2E user address on `macro_wallets`

**Files:**
- Create: `macro-markets/back/tests/Feature/CryptoCustody/DepositAddressMacroWalletsE2ETest.php` beside `tests/Unit/Graphql/Finance/DepositAddressTest.php`

Same entrypoint as the existing deposit-address test: GraphQL `depositAddress` on `Deposit::depositAddress`. Active provider is `Currency::PROVIDER_MACRO_WALLETS`. The Goravel process from Task 18 is up. `Http::fake()` does not cover `macro_wallets_base_url`.

- [ ] **Step 1: Write the scenario**

A logged-in user with a wallet for `Currency::USDT` on network `Currency::CHAIN_POLYGON` calls `depositAddress`. The outbound request is `POST /api/v1/wallets/{id}/addresses`. The GraphQL payload `address` equals the Wallets response. The new `wallet_addresses` row has `provider` = `Currency::PROVIDER_MACRO_WALLETS`. No request URL contains `express_url`.

- [ ] **Step 2: Run `php artisan test tests/Feature/CryptoCustody/DepositAddressMacroWalletsE2ETest.php`**

Expected: exit 0. This file’s pass does not stand in for Task 21.

- [ ] **Step 3: Commit `test: create a user deposit address through Macro Wallets`**

### Task 21: E2E user address on BitGo (backward compatible)

**Files:**
- Create: `macro-markets/back/tests/Feature/CryptoCustody/DepositAddressBitGoE2ETest.php` beside `tests/Unit/Graphql/Finance/DepositAddressTest.php` and `tests/Unit/Graphql/Finance/BitGoWalletTest.php`

Separate scenario from Task 20. Same GraphQL `depositAddress` entrypoint. `ActiveProvider::select(Currency::PROVIDER_BITGO)`. BitGo Express is external; fake only the Express host, the way `tests/Unit/Wallet/BitgoServiceTest.php` already does. The Wallets base URL stays unfaked so a stray call to it fails the test.

- [ ] **Step 1: Write the scenario**

`depositAddress` creates a `wallet_addresses` row with `provider` = `Currency::PROVIDER_BITGO`. `Http::assertSent` sees `{express_url}`. `Http::assertNotSent` sees `macro_wallets_base_url`. The factory class handling the call is `BitGoCustodyProvider`. Express is still the BitGo client; this scenario does not swap that URL for the Wallets API.

- [ ] **Step 2: Keep the existing BitGo address tests green**

Run, and require exit 0 on each:

- `tests/Unit/Graphql/Finance/DepositAddressTest.php`
- `tests/Unit/Graphql/Finance/BitGoWalletTest.php`
- `tests/Unit/Graphql/Finance/Withdraw/WithdrawBitGoErrorTest.php`
- `tests/Unit/Wallet/BitgoServiceTest.php`
- `tests/Unit/BitGo/BitGoWalletCreateCommandTest.php`
- `tests/Unit/BitGo/BitGoWalletRetryWalletAddressCreationTest.php`
- `tests/Feature/CryptoCustody/DepositAddressBitGoE2ETest.php`

- [ ] **Step 3: Commit `test: keep BitGo deposit address creation on Express`**

### Task 22: E2E webhooks — BitGo still credits, the other provider is ignored

**Files:**
- Create: `macro-markets/back/tests/Feature/CryptoCustody/CustodyWebhookRetrocompatE2ETest.php` beside `tests/Unit/Wallet/CustodyWebhookProviderMatchTest.php` and `tests/Unit/Services/Payment/BitGo/BitGoWebHooksTest.php`

Both routes stay live: `POST /api/bitgo/webhook` (`routes/api.php`, `BitGoWebhookController`) and `POST /api/macro-wallets/webhook` (`MacroWalletsWebhookController`). This file posts to those controllers the way `CustodyWebhookProviderMatchTest` does (`$this->post`). It is a separate scenario from Task 18. Reuse `bitgoTransferPayload()` from `tests/Unit/Wallet/CustodyWebhookProviderMatchTest.php` for the BitGo body. The Macro Wallets body uses `type` `deposit.confirmed`, `data.to_address`, and `data.asset` = `models.SymbolUSDT` (`Currency::USDT` on the ledger). Sign it with hex HMAC-SHA256 and header `X-Vault-Signature`, matching `webhook.Service.Deliver`.

- [ ] **Step 1: Write the posts**

- Address `provider` = `Currency::PROVIDER_BITGO`, `POST /api/bitgo/webhook`: 200 and one new ledger row.
- That same BitGo address, `POST /api/macro-wallets/webhook` with a valid signature: 200 `{message: ignored}` and the ledger count stays the same.
- Address `provider` = `Currency::PROVIDER_MACRO_WALLETS`, `POST /api/bitgo/webhook`: 200 `{message: ignored}` and the ledger count stays the same.
- A missing route is a failure. Both posts return 200 from the controllers above, including the ignored cases.

Polygon native on a matching Macro Wallets payload uses `models.NativePOL` and credits `Currency::POL`. This scenario does not send a MATIC asset string.

- [ ] **Step 2: Keep the existing webhook tests green**

Run, and require exit 0 on each:

- `tests/Unit/Wallet/CustodyWebhookProviderMatchTest.php`
- `tests/Unit/Services/Payment/BitGo/BitGoWebHooksTest.php`
- `tests/Feature/CryptoCustody/CustodyWebhookRetrocompatE2ETest.php`

- [ ] **Step 3: Commit `test: keep both custody webhooks live and provider-matched`**

**Green for Section D:** Tasks 18–22 each exit 0, and the BitGo files named in Tasks 21 and 22 still exit 0. Task 18’s Go test runs under `MARKETS_WEBHOOK_URL` and passes. `make test` in `macro-wallets/back` stays the in-process suite.

---

## Self-review

Spec coverage:

| Spec requirement | Task |
|---|---|
| Withdrawal accepts `asset` and uses token decimals (USDT/USDC = 6) | Task 3 |
| Omitted `asset` stays native | Task 3 |
| SOL and BTC `SignTransaction`, SOL broadcast, REST BTC broadcast | Tasks 6 and 7 |
| Executor actually calls those methods; EVM path unchanged | Task 8 |
| Sweep/consolidate no longer EVM-only; no gas seed on SOL/BTC | Task 9 |
| SOL `GetTokenBalance` via ATA; missing account is zero | Task 5 |
| Alchemy and Helius symbol plus seed decimals; unknown contract skipped | Task 4 |
| EVM scanner left as-is | Task 4 note |
| Polygon native asset is `POL`; no MATIC alias | Tasks 1 and 3 |
| Official asset, chain, token, and provider constants | Tasks 1 and 11 |
| SOL scanner destination caveat documented, `ScanBlock` not rewritten | Task 6 note and spec |
| Markets flags, factory, settings page, rename, address provider, dual webhooks | Tasks 11–17 |
| One Macro Wallets wallet per chain, no deposit/withdraw pair | Task 14 |
| Other wallets listed and unscheduled | Section C |
| Real HTTP contract: address, `asset`, chain id, `X-Vault-Signature`, provider match | Task 18 |
| Admin `settings/crypto-custody` selects `macro_wallets`, connection test hits `GET /api/v1/chains` | Task 19 |
| User deposit address on `macro_wallets` | Task 20 |
| User deposit address on `bitgo` still uses Express | Task 21 |
| BitGo webhook credits a `bitgo` address; the other webhook ignores it, and BitGo ignores a `macro_wallets` address | Task 22 |

Placeholder scan: no `TBD`, `TODO`, or “implement later” inside Section A. Section B names files, columns, and the fail-closed rule. Section C is an explicit exclusion list, not a task. Section D names the Pest files, the Go delivery test, the boot commands, and what a green run asserts.

Type names used across tasks: `ResolveWithdrawalAmount`, `ResolvedWithdrawal.WalletAsset`, `FindTokenByContract`, `AmountIsHuman`, `HumanAmount`, `ReconstructSecp256k1PrivateKey`, `localSignChain`.
