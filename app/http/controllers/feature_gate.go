package controllers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
)

// AccountIDForWallet is the account whose flag gates the wallet's money
// movement. The wallet's own account wins; the caller account is the fallback.
func AccountIDForWallet(ctx contractshttp.Context, wallet *models.Wallet) uuid.UUID {
	if wallet != nil && wallet.AccountID != nil && *wallet.AccountID != uuid.Nil {
		return *wallet.AccountID
	}
	if ctx == nil {
		return uuid.Nil
	}
	accountID, _ := ctx.Value("account_id").(uuid.UUID)
	return accountID
}

// BlockFlag asks the shared account-flag gate. A stored enabled flag is HTTP
// 409 with {"error":{"code","message"}}. A missing row or enabled=false
// returns nil so the handler continues. A read failure is a generic 500.
func BlockFlag(ctx contractshttp.Context, flags *features.Service, accountID uuid.UUID, key, code, endpoint string) contractshttp.Response {
	if flags == nil {
		return MapInternalError(ctx, fmt.Errorf("%s: feature flags are not configured", endpoint), endpoint)
	}
	err := flags.Gate(ctx.Context(), accountID, key, code)
	if err == nil {
		return nil
	}
	var gate *features.GateError
	if errors.As(err, &gate) && gate != nil && gate.Code != "" {
		return responses.Send(ctx, http.StatusConflict, contractshttp.Json{
			"error": gate.Code,
			"code":  gate.Code,
		})
	}
	return MapInternalError(ctx, err, endpoint)
}
