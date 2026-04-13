# Enhanced Withdrawal Flow — Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Replace example commands with this repo's real tools (package manager, test runner, linter).

**Goal:** Replace the single-step withdrawal form with a two-step flow (form → confirmation with fee estimate, wallet passphrase, and mandatory TOTP authentication).

**Architecture:** Backend adds `EstimateFee` to the `Chain` interface, new TOTP enrollment endpoints (`/v1/users/me/totp/`), and upgrades `CreateWalletWithdrawal` with passphrase + TOTP verification. Frontend refactors `WithdrawTab` into two steps (`WithdrawForm` → `WithdrawConfirm`), adds client-side address validation via `multicoin-address-validator`, and reuses the existing `TwoFactorSetup` component pattern for inline TOTP onboarding.

**Tech Stack:** Go 1.22 / Goravel v1.17, React 19 / TypeScript / vinext, `multicoin-address-validator` (npm), `pquerna/otp` (Go, already installed)

---

## File Map

### Backend — Create

| File | Responsibility |
|------|----------------|
| `app/http/requests/estimate_withdrawal_request.go` | Request DTO for fee estimation |
| `app/http/requests/confirm_two_factor_request.go` | Request DTO for TOTP confirm |

### Backend — Modify

| File | Change |
|------|--------|
| `pkg/types/types.go` | Add `FeeEstimate` struct, add `EstimateFee` to `Chain` interface |
| `app/services/chain/evm.go` | Implement `EstimateFee` for EVM chains |
| `app/services/chain/bitcoin.go` | Implement `EstimateFee` for Bitcoin |
| `app/services/chain/solana.go` | Implement `EstimateFee` for Solana |
| `app/http/controllers/wallet_withdrawals_controller.go` | Add `EstimateWithdrawalFee` handler, upgrade `CreateWalletWithdrawal` |
| `app/http/controllers/user_controller.go` | Add `SetupTOTP`, `ConfirmTOTP`, `DisableTOTP` handlers |
| `app/http/requests/create_wallet_withdrawal_request.go` | Add `passphrase` + `totp_code` fields |
| `app/repositories/user_repository.go` | Add `UpdateTotpSecret`, `EnableTotp`, `DisableTotp` methods |
| `app/repositories/totp_recovery_code_repository.go` | Add `CreateBatch`, `DeleteByUserID` methods |
| `routes/admin.go` | Register TOTP + fee estimate routes |

### Frontend — Create

| File | Responsibility |
|------|----------------|
| `src/utils/validateAddress.ts` | Client-side per-chain address validation |
| `src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx` | Step 1: enhanced form with real-time validation |
| `src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx` | Step 2: confirmation + auth screen |

### Frontend — Modify

| File | Change |
|------|--------|
| `package.json` | Add `multicoin-address-validator` dependency |
| `src/types/api.ts` | Add `FeeEstimateRequest`, `FeeEstimateResponse`, update `WithdrawRequest` |
| `src/lib/api/client.ts` | Add `withdrawals.estimate` method |
| `src/components/Modals/Wallet/Withdraw/index.tsx` | Refactor to two-step orchestrator |

---

## Task 1: Add `FeeEstimate` struct and `EstimateFee` to Chain interface

**Files:**
- Modify: `pkg/types/types.go:33` (after Chain interface closing brace, and inside the interface)

- [ ] **Step 1: Add `FeeEstimate` struct to types.go**

Add after the `Chain` interface (after line 33) and insert `EstimateFee` into the interface:

```go
// In pkg/types/types.go — add to the Chain interface (between line 31 and 32):
EstimateFee(ctx context.Context, req TransferRequest) (*FeeEstimate, error)
```

```go
// In pkg/types/types.go — add after the Chain interface (after line 33):

type FeeEstimate struct {
	Fee      string `json:"fee"`
	FeeAsset string `json:"fee_asset"`
	GasPrice string `json:"gas_price,omitempty"`
	GasLimit uint64 `json:"gas_limit,omitempty"`
}
```

- [ ] **Step 2: Verify compilation fails on chain adapters**

Run: `cd back && go build ./...`
Expected: compilation errors in `evm.go`, `bitcoin.go`, `solana.go` because they don't implement `EstimateFee` yet.

- [ ] **Step 3: Commit**

```bash
git add pkg/types/types.go
git commit -m "feat(types): add FeeEstimate struct and EstimateFee to Chain interface"
```

---

## Task 2: Implement `EstimateFee` for EVM chains

**Files:**
- Modify: `app/services/chain/evm.go` (add method after `BuildTransfer`, around line 124)

- [ ] **Step 1: Add `EstimateFee` method to `EVMLive`**

Insert after `BuildTransfer` (after line 124):

```go
func (a *EVMLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	var hexGas string
	if err := a.rpc.Call(ctx, "eth_gasPrice", &hexGas); err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}

	gasPrice := hexToBigInt(hexGas)
	gasLimit := uint64(21000)
	if req.Token != nil {
		gasLimit = 65000
	}

	fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, a.cfg.NativeDecimal),
		FeeAsset: a.cfg.NativeSymbol,
		GasPrice: gasPrice.String(),
		GasLimit: gasLimit,
	}, nil
}
```

- [ ] **Step 2: Verify EVM compiles**

Run: `cd back && go build ./app/services/chain/`
Expected: Still fails (Bitcoin, Solana missing), but no errors on evm.go specifically.

- [ ] **Step 3: Commit**

```bash
git add app/services/chain/evm.go
git commit -m "feat(chain): implement EstimateFee for EVM adapter"
```

---

## Task 3: Implement `EstimateFee` for Bitcoin

**Files:**
- Modify: `app/services/chain/bitcoin.go` (add method after `ValidateAddress`, around line 63)

- [ ] **Step 1: Add `EstimateFee` method to `BitcoinLive`**

Insert after `ValidateAddress` (after line 63):

```go
func (a *BitcoinLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	// Standard P2WPKH transaction ~140 vbytes
	const estimatedVBytes = 140
	feeRateSatPerVByte := 10 // default fallback

	if a.cfg.FeeRateDefault > 0 {
		feeRateSatPerVByte = a.cfg.FeeRateDefault
	}

	feeSat := int64(estimatedVBytes * feeRateSatPerVByte)
	fee := new(big.Int).SetInt64(feeSat)

	symbol := "BTC"
	if a.cfg.IsTestnet {
		symbol = "TBTC"
	}

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, 8),
		FeeAsset: symbol,
	}, nil
}
```

Also add `FeeRateDefault int` to `BitcoinConfig` if not present. Check the `BitcoinConfig` struct and add:

```go
// In BitcoinConfig struct:
FeeRateDefault int // sat/vByte fallback when no external API
```

- [ ] **Step 2: Verify Bitcoin compiles**

Run: `cd back && go build ./app/services/chain/`
Expected: Still fails on Solana, but bitcoin.go compiles.

- [ ] **Step 3: Commit**

```bash
git add app/services/chain/bitcoin.go
git commit -m "feat(chain): implement EstimateFee for Bitcoin adapter"
```

---

## Task 4: Implement `EstimateFee` for Solana

**Files:**
- Modify: `app/services/chain/solana.go` (add method after `ValidateAddress`, around line 56)

- [ ] **Step 1: Add `EstimateFee` method to `SolanaLive`**

Insert after `ValidateAddress` (after line 56):

```go
func (a *SolanaLive) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	// Solana base fee is 5000 lamports per signature (1 signature for simple transfer)
	fee := new(big.Int).SetInt64(5000)

	return &types.FeeEstimate{
		Fee:      fmtUnits(fee, 9),
		FeeAsset: a.cfg.NativeSymbol,
	}, nil
}
```

- [ ] **Step 2: Verify all chain adapters compile**

Run: `cd back && go build ./...`
Expected: PASS — all three adapters now implement `EstimateFee`.

- [ ] **Step 3: Commit**

```bash
git add app/services/chain/solana.go
git commit -m "feat(chain): implement EstimateFee for Solana adapter"
```

---

## Task 5: Fee estimation endpoint

**Files:**
- Create: `app/http/requests/estimate_withdrawal_request.go`
- Modify: `app/http/controllers/wallet_withdrawals_controller.go` (add `EstimateWithdrawalFee` handler)
- Modify: `routes/admin.go` (register route)

- [ ] **Step 1: Create request DTO**

Create `app/http/requests/estimate_withdrawal_request.go`:

```go
package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
)

type EstimateWithdrawalRequest struct {
	Amount             string `form:"amount"              json:"amount"`
	DestinationAddress string `form:"destination_address" json:"destination_address"`
}

func (r *EstimateWithdrawalRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *EstimateWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
	}
}

func (r *EstimateWithdrawalRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	walletIDStr := ctx.Request().Route("walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return nil
	}
	w, err := container.Get().WalletRepo.FindByID(walletID)
	if err != nil || w == nil {
		return nil
	}
	return data.Set("_chain", w.Chain)
}
```

- [ ] **Step 2: Add `EstimateWithdrawalFee` handler**

Add to `app/http/controllers/wallet_withdrawals_controller.go` (before `CreateWalletWithdrawal`):

```go
// EstimateWithdrawalFee godoc
// @Summary      Estimate network fee for a withdrawal
// @Description  Returns an estimated network fee. Non-blocking — errors return a structured response the frontend can handle.
// @Tags         Wallet Withdrawals
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                       true  "Wallet UUID"
// @Param        request   body      EstimateWithdrawalSwagger    true  "Estimate payload"
// @Success      200  {object}  types.FeeEstimate
// @Failure      422  {object}  ErrorResponse
// @Router       /wallets/{walletId}/withdrawals/estimate [post]
func EstimateWithdrawalFee(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	var req requests.EstimateWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	adapter, err := container.Get().Registry.Chain(wallet.Chain)
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "fee estimation unavailable",
			"code":  "FEE_ESTIMATE_FAILED",
		})
	}

	estimate, err := adapter.EstimateFee(ctx.Context(), types.TransferRequest{
		From:  "",
		To:    req.DestinationAddress,
		Asset: adapter.NativeAsset(),
	})
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "fee estimation unavailable",
			"code":  "FEE_ESTIMATE_FAILED",
		})
	}

	return ctx.Response().Json(http.StatusOK, estimate)
}
```

Add the required import for `types` at the top of the file:

```go
import (
	// ... existing imports ...
	"github.com/macrowallets/waas/pkg/types"
)
```

Also add the swagger type:

```go
type EstimateWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"tb1q..."`
}
```

- [ ] **Step 3: Register route**

In `routes/admin.go`, inside the `/{walletId}` group (after line 106, after the cancel withdrawal route):

```go
r.Post("/withdrawals/estimate", controllers.EstimateWithdrawalFee)
```

**Important:** This must be registered BEFORE the `/{withdrawalId}` routes to avoid route conflicts. Place it right after `r.Post("/withdrawals", controllers.CreateWalletWithdrawal)` (line 104):

```go
r.Post("/withdrawals", controllers.CreateWalletWithdrawal)
r.Post("/withdrawals/estimate", controllers.EstimateWithdrawalFee)
```

- [ ] **Step 4: Verify compilation**

Run: `cd back && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/http/requests/estimate_withdrawal_request.go \
       app/http/controllers/wallet_withdrawals_controller.go \
       routes/admin.go
git commit -m "feat(api): add POST /withdrawals/estimate endpoint"
```

---

## Task 6: TOTP enrollment endpoints (backend)

**Files:**
- Create: `app/http/requests/confirm_two_factor_request.go`
- Modify: `app/http/controllers/user_controller.go`
- Modify: `app/repositories/user_repository.go`
- Modify: `app/repositories/totp_recovery_code_repository.go`
- Modify: `routes/admin.go`

- [ ] **Step 1: Add repository methods**

In `app/repositories/user_repository.go`, add to the interface (after line 19):

```go
UpdateTotpSecret(id uuid.UUID, secret string) error
EnableTotp(id uuid.UUID) error
DisableTotp(id uuid.UUID) error
```

Add implementations:

```go
func (r *userRepository) UpdateTotpSecret(id uuid.UUID, secret string) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("totp_secret", secret)
	return err
}

func (r *userRepository) EnableTotp(id uuid.UUID) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Update("totp_enabled", true)
	return err
}

func (r *userRepository) DisableTotp(id uuid.UUID) error {
	_, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"totp_enabled": false,
		"totp_secret":  "",
	})
	return err
}
```

In `app/repositories/totp_recovery_code_repository.go`, add to the interface (after line 14):

```go
CreateBatch(codes []models.TotpRecoveryCode) error
DeleteByUserID(userID uuid.UUID) error
```

Add implementations:

```go
func (r *totpRecoveryCodeRepository) CreateBatch(codes []models.TotpRecoveryCode) error {
	return facades.Orm().Query().Create(&codes)
}

func (r *totpRecoveryCodeRepository) DeleteByUserID(userID uuid.UUID) error {
	_, err := facades.Orm().Query().Where("user_id = ?", userID).Delete(&models.TotpRecoveryCode{})
	return err
}
```

- [ ] **Step 2: Create TOTP confirm request DTO**

Create `app/http/requests/confirm_two_factor_request.go`:

```go
package requests

import "github.com/goravel/framework/contracts/http"

type ConfirmTwoFactorRequest struct {
	Code string `form:"code" json:"code"`
}

func (r *ConfirmTwoFactorRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *ConfirmTwoFactorRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"code": "required|min_len:6|max_len:6",
	}
}
```

- [ ] **Step 3: Add controller handlers**

Add to `app/http/controllers/user_controller.go`:

```go
// SetupTOTP godoc
// @Summary      Start TOTP 2FA setup
// @Description  Generates a TOTP secret and returns the QR URL for scanning
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  TotpSetupSwagger
// @Failure      401  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse
// @Router       /users/me/totp/setup [post]
func SetupTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	secret, qrURL, err := userAuthService.GenerateTOTP(user.Email)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to generate TOTP secret"})
	}

	encryptedSecret, err := facades.Crypt().EncryptString(secret)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to encrypt secret"})
	}

	if err := container.Get().UserRepo.UpdateTotpSecret(user.ID, encryptedSecret); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to save TOTP secret"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"secret": secret,
		"qr_url": qrURL,
	})
}

// ConfirmTOTP godoc
// @Summary      Confirm TOTP setup
// @Description  Verifies the TOTP code, enables 2FA, and returns recovery codes
// @Tags         User
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request  body      ConfirmTotpSwagger  true  "TOTP code"
// @Success      200  {object}  models.User
// @Failure      400  {object}  ErrorResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/totp/verify [post]
func ConfirmTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	var req requests.ConfirmTwoFactorRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if user.TotpSecret == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "no TOTP secret found — call setup first"})
	}

	decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to decrypt secret"})
	}

	if !userAuthService.VerifyTOTP(decryptedSecret, req.Code) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid verification code"})
	}

	if err := container.Get().UserRepo.EnableTotp(user.ID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to enable 2FA"})
	}

	codes, hashes, err := userAuthService.GenerateRecoveryCodes()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to generate recovery codes"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(user.ID)

	var recoveryCodes []models.TotpRecoveryCode
	for _, h := range hashes {
		recoveryCodes = append(recoveryCodes, models.TotpRecoveryCode{
			ID:       uuid.New(),
			UserID:   user.ID,
			CodeHash: h,
		})
	}
	_ = container.Get().TotpRecoveryCodeRepo.CreateBatch(recoveryCodes)

	user.TotpEnabled = true
	resp := map[string]interface{}{
		"user":           user,
		"recovery_codes": codes,
	}
	return ctx.Response().Json(http.StatusOK, resp)
}

// DisableTOTP godoc
// @Summary      Disable TOTP 2FA
// @Description  Disables two-factor authentication for the current user
// @Tags         User
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  models.User
// @Failure      401  {object}  ErrorResponse
// @Router       /users/me/totp [delete]
func DisableTOTP(ctx http.Context) http.Response {
	user := ctx.Value("user").(*models.User)

	if err := container.Get().UserRepo.DisableTotp(user.ID); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to disable 2FA"})
	}

	_ = container.Get().TotpRecoveryCodeRepo.DeleteByUserID(user.ID)

	user.TotpEnabled = false
	user.TotpSecret = ""
	return ctx.Response().Json(http.StatusOK, user)
}
```

Add the required imports (add `facades` and `uuid` if not already imported):

```go
import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)
```

Add swagger types at the bottom:

```go
type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
```

- [ ] **Step 4: Register routes**

In `routes/admin.go`, add TOTP routes inside the `/v1/users` group (after line 32):

```go
router.Post("/me/totp/setup", controllers.SetupTOTP)
router.Post("/me/totp/verify", controllers.ConfirmTOTP)
router.Delete("/me/totp", controllers.DisableTOTP)
```

- [ ] **Step 5: Verify compilation**

Run: `cd back && go build ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add app/http/requests/confirm_two_factor_request.go \
       app/http/controllers/user_controller.go \
       app/repositories/user_repository.go \
       app/repositories/totp_recovery_code_repository.go \
       routes/admin.go
git commit -m "feat(api): add TOTP setup/confirm/disable endpoints"
```

---

## Task 7: Upgrade `CreateWalletWithdrawal` with passphrase + TOTP

**Files:**
- Modify: `app/http/requests/create_wallet_withdrawal_request.go`
- Modify: `app/http/controllers/wallet_withdrawals_controller.go`

- [ ] **Step 1: Add fields to request DTO**

Replace `app/http/requests/create_wallet_withdrawal_request.go` content:

```go
package requests

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
)

type CreateWalletWithdrawalRequest struct {
	Amount             string `form:"amount"              json:"amount"`
	DestinationAddress string `form:"destination_address" json:"destination_address"`
	Note               string `form:"note"                json:"note,omitempty"`
	Passphrase         string `form:"passphrase"          json:"passphrase"`
	TotpCode           string `form:"totp_code"           json:"totp_code"`
}

func (r *CreateWalletWithdrawalRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWalletWithdrawalRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"amount":              "required|decimal_string",
		"destination_address": "required|blockchain_address",
		"passphrase":          "required|min_len:12",
		"totp_code":           "required|min_len:6|max_len:6",
	}
}

func (r *CreateWalletWithdrawalRequest) PrepareForValidation(ctx http.Context, data validation.Data) error {
	walletIDStr := ctx.Request().Route("walletId")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return nil
	}
	w, err := container.Get().WalletRepo.FindByID(walletID)
	if err != nil || w == nil {
		return nil
	}
	return data.Set("_chain", w.Chain)
}
```

- [ ] **Step 2: Upgrade `CreateWalletWithdrawal` handler**

Replace the `CreateWalletWithdrawal` function in `app/http/controllers/wallet_withdrawals_controller.go`:

```go
func CreateWalletWithdrawal(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	callerID, _ := ctx.Value("user_id").(uuid.UUID)

	var req requests.CreateWalletWithdrawalRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	// Load user for TOTP verification
	user, err := container.Get().UserRepo.FindByID(callerID)
	if err != nil || user == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
	}

	// Enforce 2FA is enabled
	if !user.TotpEnabled {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "2FA must be enabled before withdrawing"})
	}

	// Verify TOTP code
	decryptedSecret, err := facades.Crypt().DecryptString(user.TotpSecret)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	authService := authsvc.NewService()
	if !authService.VerifyTOTP(decryptedSecret, req.TotpCode) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	}

	// Verify passphrase via MPC share decryption
	if err := verifyWalletPassphrase(ctx, wallet, req.Passphrase); err != nil {
		return err
	}

	w := &models.Withdrawal{
		ID:                 uuid.New(),
		WalletID:           wallet.ID,
		Status:             "pending",
		Amount:             req.Amount,
		DestinationAddress: req.DestinationAddress,
		Note:               req.Note,
		CreatedBy:          &callerID,
	}
	if wallet.AccountID != nil {
		w.AccountID = wallet.AccountID
	}

	if err := container.Get().WithdrawalRepo.Create(w); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create withdrawal"})
	}
	return ctx.Response().Json(http.StatusCreated, w)
}
```

Add the `verifyWalletPassphrase` helper in the same file:

```go
func verifyWalletPassphrase(ctx http.Context, wallet *models.Wallet, passphrase string) http.Response {
	rdb := container.Get().Redis

	// Check rate limit
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", wallet.ID)
	count, err := rdb.Get(ctx.Context(), key).Int()
	if err == nil && count >= 5 {
		return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": "too many failed attempts, try again later"})
	}

	ciphertext, err := hex.DecodeString(wallet.MPCCustomerShare)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	iv, err := hex.DecodeString(wallet.MPCShareIV)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	salt, err := hex.DecodeString(wallet.MPCShareSalt)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}

	enc := &mpcpkg.EncryptedShare{
		Ciphertext: ciphertext,
		IV:         iv,
		Salt:       salt,
	}
	_, decErr := mpcpkg.DecryptShare(enc, passphrase)
	if decErr != nil {
		// Record failed attempt
		pipe := rdb.Pipeline()
		pipe.Incr(ctx.Context(), key)
		pipe.Expire(ctx.Context(), key, 60*time.Second)
		_, _ = pipe.Exec(ctx.Context())
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid passphrase"})
	}

	return nil
}
```

Add the required imports at the top:

```go
import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)
```

- [ ] **Step 3: Update swagger type**

Update `CreateWalletWithdrawalSwagger`:

```go
type CreateWalletWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"bc1q..."`
	Note               string `json:"note,omitempty" example:"Monthly payment"`
	Passphrase         string `json:"passphrase" example:"my-secure-wallet-passphrase"`
	TotpCode           string `json:"totp_code" example:"123456"`
}
```

- [ ] **Step 4: Verify compilation**

Run: `cd back && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/http/requests/create_wallet_withdrawal_request.go \
       app/http/controllers/wallet_withdrawals_controller.go
git commit -m "feat(api): require passphrase + TOTP for wallet withdrawals"
```

---

## Task 8: Install `multicoin-address-validator` (frontend)

**Files:**
- Modify: `front/package.json`

- [ ] **Step 1: Install dependency**

```bash
cd front && npm install multicoin-address-validator
```

- [ ] **Step 2: Commit**

```bash
git add package.json package-lock.json
git commit -m "chore(deps): add multicoin-address-validator for client-side address validation"
```

---

## Task 9: Create address validation utility

**Files:**
- Create: `front/src/utils/validateAddress.ts`

- [ ] **Step 1: Create the utility**

Create `front/src/utils/validateAddress.ts`:

```typescript
import WAValidator from "multicoin-address-validator";

const CHAIN_TO_COIN: Record<string, { coin: string; networkType?: string }> = {
  ETH: { coin: "eth" },
  POLYGON: { coin: "eth" },
  BSC: { coin: "eth" },
  AVAX: { coin: "eth" },
  ARB: { coin: "eth" },
  OP: { coin: "eth" },
  BTC: { coin: "btc" },
  TBTC: { coin: "btc", networkType: "testnet" },
  SOL: { coin: "sol" },
};

export function validateAddress(address: string, chain: string): boolean {
  if (!address.trim()) return false;
  const mapping = CHAIN_TO_COIN[chain.toUpperCase()];
  if (!mapping) return false;
  try {
    return WAValidator.validate(address, mapping.coin, mapping.networkType ?? "prod");
  } catch {
    return false;
  }
}

export function getAddressPlaceholder(chain: string): string {
  const upper = chain.toUpperCase();
  if (upper === "TBTC") return "tb1q... or m.../n...";
  if (upper === "BTC") return "bc1q... or 1.../3...";
  if (upper === "SOL") return "Solana address";
  return "0x... (EVM address)";
}
```

- [ ] **Step 2: Commit**

```bash
git add src/utils/validateAddress.ts
git commit -m "feat(utils): add per-chain address validation utility"
```

---

## Task 10: Update API types and client

**Files:**
- Modify: `front/src/types/api.ts`
- Modify: `front/src/lib/api/client.ts`

- [ ] **Step 1: Update API types**

In `front/src/types/api.ts`, update `WithdrawRequest` (replace lines 83-87):

```typescript
export interface WithdrawRequest {
  amount: string;
  destination_address: string;
  note?: string;
  passphrase: string;
  totp_code: string;
}
```

Add new types after `WithdrawRequest`:

```typescript
export interface FeeEstimateRequest {
  amount: string;
  destination_address: string;
}

export interface FeeEstimateResponse {
  fee: string;
  fee_asset: string;
  gas_price?: string;
  gas_limit?: number;
}
```

- [ ] **Step 2: Add fee estimate API method**

In `front/src/lib/api/client.ts`, add inside the `withdrawals` object (after `create`, around line 264):

```typescript
      estimate: (walletId: string, body: { amount: string; destination_address: string }) =>
        authedRequest<{ fee: string; fee_asset: string; gas_price?: string; gas_limit?: number }>(
          "POST",
          `/v1/wallets/${walletId}/withdrawals/estimate`,
          body,
        ),
```

- [ ] **Step 3: Commit**

```bash
git add src/types/api.ts src/lib/api/client.ts
git commit -m "feat(api): add fee estimate method and update WithdrawRequest types"
```

---

## Task 11: Create `WithdrawForm` component (Step 1)

**Files:**
- Create: `front/src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx`

- [ ] **Step 1: Create the form component**

Create `front/src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx`:

```tsx
"use client";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ShieldAlert } from "lucide-react";
import { cn } from "@/utils/cn";
import { validateAddress, getAddressPlaceholder } from "@/utils/validateAddress";
import { isUtxoChain } from "@/types/wallet";
import type { Wallet } from "@/types/wallet";
import {
  primaryButton,
  cancelButton,
  inputNormal,
  inputError,
  fieldLabel,
  fieldError,
} from "@/components/Modals/sharedModalUi";
import {
  walletModalPanelClassName,
  walletModalCalloutClassName,
  walletModalWarningClassName,
  walletChainBadgeClassName,
} from "@/components/Modals/walletModalUi";

export interface WithdrawFormData {
  amount: string;
  destination: string;
  note: string;
}

interface WithdrawFormProps {
  wallet: Wallet;
  initialData?: WithdrawFormData;
  onContinue: (data: WithdrawFormData) => void;
  onCancel: () => void;
}

export function WithdrawForm({ wallet, initialData, onContinue, onCancel }: WithdrawFormProps) {
  const { t } = useTranslation("wallet");
  const [amount, setAmount] = useState(initialData?.amount ?? "");
  const [destination, setDestination] = useState(initialData?.destination ?? "");
  const [note, setNote] = useState(initialData?.note ?? "");
  const [touched, setTouched] = useState<Record<string, boolean>>({});

  const chain = wallet.chain ?? "";
  const frozen = wallet.status === "frozen";

  const markTouched = (f: string) => setTouched((p) => ({ ...p, [f]: true }));

  const fieldErrors = useMemo(() => {
    const amountNum = Number(amount);
    const balanceNum = Number(wallet.balance ?? "0");
    return {
      amount: touched.amount
        ? !amount || amountNum <= 0
          ? t("withdraw.errorAmount")
          : amountNum > balanceNum
            ? t("withdraw.errorInsufficientBalance", "Insufficient balance")
            : ""
        : "",
      destination: touched.destination
        ? !destination.trim()
          ? t("withdraw.errorDestination")
          : !validateAddress(destination.trim(), chain)
            ? t("withdraw.errorInvalidAddress", { chain })
            : ""
        : "",
    };
  }, [touched, amount, destination, chain, wallet.balance, t]);

  const handleAmountChange = (val: string) => {
    const cleaned = val.replace(/[^0-9.]/g, "");
    const parts = cleaned.split(".");
    if (parts.length > 2) return;
    setAmount(cleaned);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched({ amount: true, destination: true });

    const amountNum = Number(amount);
    if (!amount || amountNum <= 0) return;
    if (!destination.trim() || !validateAddress(destination.trim(), chain)) return;

    onContinue({ amount, destination: destination.trim(), note: note.trim() });
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className={cn(walletModalPanelClassName, "flex items-center justify-between")}>
        <div className="flex items-center gap-2">
          <span className={walletChainBadgeClassName}>{wallet.chain}</span>
          {wallet.label && (
            <span className="text-sm font-medium text-p-primary dark:text-p-primary-dark">
              {wallet.label}
            </span>
          )}
        </div>
        <div className="text-right">
          <p className="text-xs font-semibold uppercase tracking-wide text-p-hint dark:text-p-hint-dark">
            {t("withdraw.available")}
          </p>
          <p className="font-mono text-lg font-semibold text-p-primary dark:text-p-primary-dark">
            {wallet.balance ?? "0"}
          </p>
        </div>
      </div>

      {frozen && (
        <div className={cn(walletModalWarningClassName, "flex gap-2")}>
          <ShieldAlert className="mt-0.5 h-5 w-5 shrink-0" />
          <div>
            <p className="font-semibold">{t("withdraw.frozenTitle")}</p>
            <p className="mt-1 opacity-90">{t("withdraw.frozenBody")}</p>
          </div>
        </div>
      )}

      <div className={cn(walletModalCalloutClassName, "flex gap-2")}>
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-primary dark:text-primary-dark" />
        <p className="text-p-hint dark:text-p-hint-dark">
          <strong className="text-p-primary dark:text-p-primary-dark">
            {t("withdraw.warningIrreversible")}
          </strong>
        </p>
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className={fieldLabel}>{t("withdraw.amount")}</label>
          {wallet.balance && !frozen && (
            <button
              type="button"
              className="text-xs font-semibold text-primary dark:text-primary-dark"
              onClick={() => setAmount(wallet.balance ?? "")}
            >
              {t("withdraw.useMax")}
            </button>
          )}
        </div>
        <input
          type="text"
          inputMode="decimal"
          value={amount}
          onChange={(e) => handleAmountChange(e.target.value)}
          onBlur={() => markTouched("amount")}
          placeholder={t("withdraw.amountPlaceholder")}
          disabled={frozen}
          className={cn("font-mono", fieldErrors.amount ? inputError : inputNormal)}
        />
        {fieldErrors.amount && <p className={fieldError}>{fieldErrors.amount}</p>}
      </div>

      <div>
        <label className={fieldLabel}>{t("withdraw.destination")}</label>
        <input
          type="text"
          value={destination}
          onChange={(e) => setDestination(e.target.value)}
          onBlur={() => markTouched("destination")}
          placeholder={getAddressPlaceholder(chain)}
          disabled={frozen}
          className={cn("font-mono text-sm", fieldErrors.destination ? inputError : inputNormal)}
        />
        {fieldErrors.destination && <p className={fieldError}>{fieldErrors.destination}</p>}
      </div>

      <div>
        <label className={fieldLabel}>
          {t("withdraw.note")}{" "}
          <span className="font-normal text-p-hint dark:text-p-hint-dark">
            {t("withdraw.noteOptional")}
          </span>
        </label>
        <input
          type="text"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder={t("withdraw.notePlaceholder")}
          disabled={frozen}
          className={inputNormal}
        />
      </div>

      <div className="flex gap-3 border-t border-divider dark:border-divider-dark pt-4">
        <button type="button" onClick={onCancel} className={cn("flex-1", cancelButton)}>
          {t("withdraw.cancel")}
        </button>
        <button type="submit" disabled={frozen} className={cn("flex-1", primaryButton)}>
          {t("withdraw.continue", "Continue")}
        </button>
      </div>
    </form>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx
git commit -m "feat(ui): add WithdrawForm component with per-chain address validation"
```

---

## Task 12: Create `WithdrawConfirm` component (Step 2)

**Files:**
- Create: `front/src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx`

- [ ] **Step 1: Create the confirmation component**

Create `front/src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx`:

```tsx
"use client";

import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, ChevronLeft, Loader2, Info } from "lucide-react";
import { cn } from "@/utils/cn";
import { api } from "@/lib/api";
import { toast } from "react-toastify";
import { useAuth } from "@/hooks/useAuth";
import { useDispatch } from "react-redux";
import { setUser } from "@/lib/store/auth.slice";
import { TwoFactorSetup } from "@/components/Profile/TwoFactorSetup";
import type { Wallet } from "@/types/wallet";
import type { FeeEstimateResponse } from "@/types/api";
import type { WithdrawFormData } from "./WithdrawForm";
import {
  primaryButton,
  cancelButton,
  inputNormal,
  inputError,
  fieldLabel,
  fieldError,
} from "@/components/Modals/sharedModalUi";
import {
  walletModalPanelClassName,
  walletChainBadgeClassName,
} from "@/components/Modals/walletModalUi";

interface WithdrawConfirmProps {
  wallet: Wallet;
  formData: WithdrawFormData;
  feeEstimate: FeeEstimateResponse | null;
  onBack: () => void;
  onClose: () => void;
}

export function WithdrawConfirm({ wallet, formData, feeEstimate, onBack, onClose }: WithdrawConfirmProps) {
  const { t } = useTranslation("wallet");
  const { user } = useAuth();
  const dispatch = useDispatch();
  const [passphrase, setPassphrase] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [error, setError] = useState("");
  const [fieldErrs, setFieldErrs] = useState<Record<string, string>>({});
  const [isLoading, setIsLoading] = useState(false);

  const totpEnabled = user?.totp_enabled ?? false;

  const truncateAddress = (addr: string) =>
    addr.length > 20 ? `${addr.slice(0, 10)}...${addr.slice(-10)}` : addr;

  const total = feeEstimate
    ? (Number(formData.amount) + Number(feeEstimate.fee)).toString()
    : null;

  const handleTotpSetupComplete = async () => {
    try {
      const updated = await api.user.me();
      dispatch(setUser(updated));
    } catch { /* user data will refresh on next render */ }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setFieldErrs({});

    const errs: Record<string, string> = {};
    if (!passphrase || passphrase.length < 12) {
      errs.passphrase = t("withdraw.errorPassphrase", "Passphrase must be at least 12 characters");
    }
    if (!totpCode || totpCode.length !== 6) {
      errs.totpCode = t("withdraw.errorTotpCode", "Enter a 6-digit code");
    }
    if (Object.keys(errs).length > 0) {
      setFieldErrs(errs);
      return;
    }

    setIsLoading(true);
    try {
      await api.wallets.withdrawals.create(wallet.id, {
        amount: formData.amount,
        destination_address: formData.destination,
        note: formData.note || undefined,
        passphrase,
        totp_code: totpCode,
      });
      toast.success(t("withdraw.success", "Withdrawal submitted"));
      onClose();
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("withdraw.errorGeneric");
      if (msg.includes("passphrase")) {
        setFieldErrs({ passphrase: msg });
      } else if (msg.includes("2FA") || msg.includes("code")) {
        setFieldErrs({ totpCode: msg });
      } else {
        setError(msg);
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="flex flex-col gap-5">
      {/* Summary */}
      <div className="flex items-center gap-3 mb-1">
        <div className="flex h-10 w-10 items-center justify-center rounded-full bg-primary/15 dark:bg-primary-dark/20">
          <ArrowUpRight size={20} className="text-primary dark:text-primary-dark" />
        </div>
        <h3 className="text-base font-semibold text-p-primary dark:text-p-primary-dark">
          {t("withdraw.confirmTitle", { amount: formData.amount, asset: wallet.balance_asset ?? wallet.chain })}
        </h3>
      </div>

      <div className={cn(walletModalPanelClassName, "space-y-3 text-sm")}>
        <div className="flex justify-between">
          <span className="text-p-hint dark:text-p-hint-dark">{t("withdraw.from", "From")}</span>
          <div className="text-right">
            <div className="flex items-center gap-1.5 justify-end">
              <span className={walletChainBadgeClassName}>{wallet.chain}</span>
              <span className="font-medium text-p-primary dark:text-p-primary-dark">{wallet.label}</span>
            </div>
          </div>
        </div>

        <div className="flex justify-between">
          <span className="text-p-hint dark:text-p-hint-dark">{t("withdraw.to", "To")}</span>
          <span className="font-mono text-p-primary dark:text-p-primary-dark" title={formData.destination}>
            {truncateAddress(formData.destination)}
          </span>
        </div>

        <div className="flex justify-between">
          <span className="text-p-hint dark:text-p-hint-dark">{t("withdraw.amount")}</span>
          <span className="font-mono font-semibold text-p-primary dark:text-p-primary-dark">
            {formData.amount} {wallet.balance_asset ?? wallet.chain}
          </span>
        </div>

        <div className="border-t border-divider dark:border-divider-dark my-1" />

        <div className="flex justify-between">
          <span className="text-p-hint dark:text-p-hint-dark">{t("withdraw.estFee", "Est. Network Fee")}</span>
          {feeEstimate ? (
            <span className="font-mono text-p-primary dark:text-p-primary-dark">
              {feeEstimate.fee} {feeEstimate.fee_asset}
            </span>
          ) : (
            <span className="flex items-center gap-1 text-p-hint dark:text-p-hint-dark">
              <Info size={14} />
              {t("withdraw.feeUnavailable", "Unavailable")}
            </span>
          )}
        </div>

        <div className="flex justify-between">
          <span className="text-p-hint dark:text-p-hint-dark font-medium">{t("withdraw.total", "Total")}</span>
          <span className="font-mono font-semibold text-p-primary dark:text-p-primary-dark">
            {total ?? formData.amount} {wallet.balance_asset ?? wallet.chain}
          </span>
        </div>
      </div>

      {/* Authentication */}
      {!totpEnabled ? (
        <div className={cn(walletModalPanelClassName, "space-y-3")}>
          <p className="text-sm font-semibold text-p-primary dark:text-p-primary-dark">
            {t("withdraw.enable2faTitle", "Enable Two-Factor Authentication")}
          </p>
          <p className="text-xs text-p-hint dark:text-p-hint-dark">
            {t("withdraw.enable2faDesc", "2FA is required for withdrawals. Set it up now to proceed.")}
          </p>
          <TwoFactorSetup user={user!} />
        </div>
      ) : (
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className={cn(walletModalPanelClassName, "space-y-4")}>
            <div>
              <p className="text-sm font-semibold text-p-primary dark:text-p-primary-dark mb-1">
                {t("withdraw.authenticate", "Authenticate")}
              </p>
              <p className="text-xs text-p-hint dark:text-p-hint-dark mb-3">
                {t("withdraw.authenticateDesc", "Enter your wallet password and 2FA code to initiate the withdrawal.")}
              </p>
            </div>

            <div>
              <label className={fieldLabel}>{t("withdraw.passphrase", "Wallet Password")}</label>
              <input
                type="password"
                value={passphrase}
                onChange={(e) => setPassphrase(e.target.value)}
                placeholder={t("withdraw.passphrasePlaceholder", "Enter wallet passphrase")}
                className={cn(fieldErrs.passphrase ? inputError : inputNormal)}
              />
              {fieldErrs.passphrase && <p className={fieldError}>{fieldErrs.passphrase}</p>}
            </div>

            <div>
              <label className={fieldLabel}>{t("withdraw.totpCode", "2FA Code")}</label>
              <input
                type="text"
                inputMode="numeric"
                maxLength={6}
                value={totpCode}
                onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ""))}
                placeholder="000000"
                className={cn("font-mono tracking-widest", fieldErrs.totpCode ? inputError : inputNormal)}
              />
              {fieldErrs.totpCode && <p className={fieldError}>{fieldErrs.totpCode}</p>}
            </div>
          </div>

          {error && (
            <p className="text-sm text-red-500 bg-red-500/10 rounded px-3 py-2">{error}</p>
          )}

          <div className="flex gap-3 border-t border-divider dark:border-divider-dark pt-4">
            <button type="button" onClick={onBack} className={cn("flex-shrink-0", cancelButton)}>
              <ChevronLeft size={16} className="mr-1 inline-block" />
              {t("withdraw.back", "Back")}
            </button>
            <button type="submit" disabled={isLoading} className={cn("flex-1", primaryButton)}>
              {isLoading ? (
                <>
                  <Loader2 size={18} className="animate-spin" />
                  {t("withdraw.submitting")}
                </>
              ) : (
                t("withdraw.initiateWithdrawal", "Initiate Withdrawal")
              )}
            </button>
          </div>
        </form>
      )}

      {/* Back button when TOTP not enabled (no form wrapping it) */}
      {!totpEnabled && (
        <div className="flex gap-3 border-t border-divider dark:border-divider-dark pt-4">
          <button type="button" onClick={onBack} className={cancelButton}>
            <ChevronLeft size={16} className="mr-1 inline-block" />
            {t("withdraw.back", "Back")}
          </button>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx
git commit -m "feat(ui): add WithdrawConfirm component with fee summary and auth"
```

---

## Task 13: Refactor `WithdrawTab` as two-step orchestrator

**Files:**
- Modify: `front/src/components/Modals/Wallet/Withdraw/index.tsx`

- [ ] **Step 1: Rewrite `WithdrawTab` as orchestrator**

Replace the entire content of `front/src/components/Modals/Wallet/Withdraw/index.tsx`:

```tsx
"use client";

import { useState, useCallback } from "react";
import { useWallet } from "@/hooks/useWallet";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/utils/cn";
import { contentPadding } from "../styles";
import { WithdrawForm } from "./WithdrawForm";
import { WithdrawConfirm } from "./WithdrawConfirm";
import type { WithdrawFormData } from "./WithdrawForm";
import type { FeeEstimateResponse } from "@/types/api";

type WithdrawStep = "form" | "confirm";

interface WithdrawTabProps {
  walletId: string;
  onClose: () => void;
}

export function WithdrawTab({ walletId, onClose }: WithdrawTabProps) {
  const { wallet, isLoading: walletLoading } = useWallet(walletId);
  const [step, setStep] = useState<WithdrawStep>("form");
  const [formData, setFormData] = useState<WithdrawFormData | null>(null);
  const [feeEstimate, setFeeEstimate] = useState<FeeEstimateResponse | null>(null);

  const handleContinue = useCallback(
    async (data: WithdrawFormData) => {
      setFormData(data);

      try {
        const estimate = await api.wallets.withdrawals.estimate(walletId, {
          amount: data.amount,
          destination_address: data.destination,
        });
        setFeeEstimate(estimate);
      } catch {
        setFeeEstimate(null);
      }

      setStep("confirm");
    },
    [walletId],
  );

  const handleBack = useCallback(() => {
    setStep("form");
  }, []);

  if (walletLoading) {
    return (
      <div className={cn(contentPadding, "space-y-3 animate-pulse")}>
        <div className="h-20 rounded-md bg-accent dark:bg-accent-dark" />
      </div>
    );
  }

  if (!wallet) {
    return (
      <p className={cn(contentPadding, "text-p-hint dark:text-p-hint-dark py-8 text-center")}>
        Could not load wallet.
      </p>
    );
  }

  return (
    <div className={contentPadding}>
      {step === "form" ? (
        <WithdrawForm
          wallet={wallet}
          initialData={formData ?? undefined}
          onContinue={handleContinue}
          onCancel={onClose}
        />
      ) : formData ? (
        <WithdrawConfirm
          wallet={wallet}
          formData={formData}
          feeEstimate={feeEstimate}
          onBack={handleBack}
          onClose={onClose}
        />
      ) : null}
    </div>
  );
}
```

- [ ] **Step 2: Verify no TypeScript errors**

Run: `cd front && npx tsc --noEmit`
Expected: PASS (or only pre-existing errors)

- [ ] **Step 3: Commit**

```bash
git add src/components/Modals/Wallet/Withdraw/index.tsx
git commit -m "feat(ui): refactor WithdrawTab into two-step form → confirmation flow"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Task |
|------------------|------|
| `EstimateFee` on Chain interface | Task 1 |
| EVM fee estimation | Task 2 |
| Bitcoin fee estimation | Task 3 |
| Solana fee estimation | Task 4 |
| `POST /withdrawals/estimate` endpoint | Task 5 |
| TOTP setup endpoint | Task 6 |
| TOTP confirm endpoint | Task 6 |
| TOTP disable endpoint | Task 6 |
| Passphrase + TOTP on `CreateWalletWithdrawal` | Task 7 |
| `multicoin-address-validator` install | Task 8 |
| Client-side address validation | Task 9 |
| Updated `WithdrawRequest` types | Task 10 |
| Fee estimate API client method | Task 10 |
| `WithdrawForm` (step 1) | Task 11 |
| `WithdrawConfirm` (step 2) | Task 12 |
| Inline TOTP setup on confirmation | Task 12 (reuses `TwoFactorSetup`) |
| Two-step orchestrator | Task 13 |

### Placeholder scan

No TBD, TODO, or vague steps found. All code blocks are complete.

### Type consistency

- `WithdrawFormData` defined in `WithdrawForm.tsx`, imported by `WithdrawConfirm.tsx` and `index.tsx`
- `FeeEstimateResponse` defined in `api.ts`, used by `WithdrawConfirm.tsx` and `index.tsx`
- `WithdrawRequest` updated in `api.ts` matches what `WithdrawConfirm.tsx` sends
- `CreateWalletWithdrawalRequest` Go struct matches the frontend's `WithdrawRequest` fields
- `FeeEstimate` Go struct matches `FeeEstimateResponse` TypeScript interface
- Backend TOTP endpoints match existing frontend API client methods (`api.user.totpSetup`, `api.user.totpVerify`, `api.user.totpDisable`)
