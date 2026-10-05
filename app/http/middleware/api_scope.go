package middleware

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/repositories"
)

// Permission names a route passes to APIScope. They are the S3.4.6 catalog.
// Routes cannot import policies, so the names are aliased here.
const (
	PermWalletsRead       = policies.PermWalletsRead
	PermWalletsCreate     = policies.PermWalletsCreate
	PermAddressesCreate   = policies.PermAddressesCreate
	PermWithdrawalsCreate = policies.PermWithdrawalsCreate
	PermSweepExecute      = policies.PermSweepExecute
	PermWebhooksRead      = policies.PermWebhooksRead
	PermWebhooksWrite     = policies.PermWebhooksWrite
	PermTransactionsRead  = policies.PermTransactionsRead
)

// APIScope limits a token that lists permissions to those names. A blank
// permissions store keeps today's access. A missing permission is 403
// forbidden: S3.4.6 does not name another code. The check reads the token
// APITokenAuth already stored. GET /transactions/{id} and
// PATCH /webhooks/{webhookId} resolve the resource first: a missing resource,
// and a resource that is not this account's, is 404 before the permission
// check. An id that is not a UUID is left to the handler, which answers 400.
func APIScope(permission string) http.Middleware {
	return func(ctx http.Context) {
		token, ok := requestctx.APIToken(ctx)
		if !ok || token == nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
			return
		}
		switch gateExternalChild(ctx) {
		case childPass:
			ctx.Request().Next()
			return
		case childAnswered:
			return
		}
		if !policies.APITokenAllows(token.Permissions, permission) {
			abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": responses.CodeForbidden})
			return
		}
		ctx.Request().Next()
	}
}

// gateExternalChild resolves the external path child before the scope check.
// A transaction or webhook that is missing or belongs to another account is
// 404 here, because the transaction handler loads by id alone.
func gateExternalChild(ctx http.Context) childGate {
	if raw := strings.TrimSpace(ctx.Request().Route("webhookId")); raw != "" {
		return gateExternalWebhook(ctx, raw)
	}
	if raw := strings.TrimSpace(ctx.Request().Route("id")); raw != "" {
		return gateExternalTransaction(ctx, raw)
	}
	return childCheck
}

func gateExternalTransaction(ctx http.Context, raw string) childGate {
	id, err := uuid.Parse(raw)
	if err != nil {
		return childPass
	}
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		return childAnswered
	}
	tx, err := container.MustMake[*repositories.TransactionRepository]().FindByIDForAccount(ctx.Context(), id, accountID)
	if err != nil && !rowMissing(err) {
		abortWithJSON(ctx, http.StatusInternalServerError, http.Json{"error": "failed to load transaction"})
		return childAnswered
	}
	if rowMissing(err) || tx == nil || tx.ID == uuid.Nil {
		abortWithJSON(ctx, http.StatusNotFound, http.Json{"error": "transaction not found"})
		return childAnswered
	}
	return childCheck
}

func gateExternalWebhook(ctx http.Context, raw string) childGate {
	id, err := uuid.Parse(raw)
	if err != nil {
		return childPass
	}
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
		return childAnswered
	}
	row, err := container.MustMake[*repositories.WebhookConfigRepository]().FindOwnership(ctx.Context(), id)
	if err != nil && !rowMissing(err) {
		abortWithJSON(ctx, http.StatusInternalServerError, http.Json{"error": "failed to load webhook"})
		return childAnswered
	}
	if rowMissing(err) || !webhookVisibleToAccount(row, accountID) {
		abortWithJSON(ctx, http.StatusNotFound, http.Json{"error": "webhook not found"})
		return childAnswered
	}
	return childCheck
}

func webhookVisibleToAccount(row *repositories.WebhookOwnership, accountID uuid.UUID) bool {
	if row == nil || row.ID == uuid.Nil || row.WalletID != nil {
		return false
	}
	if row.AccountID != nil && *row.AccountID != accountID {
		return false
	}
	return true
}
