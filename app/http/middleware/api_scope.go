package middleware

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
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

// ScopeLookups are the reads APIScope makes to resolve the path child.
type ScopeLookups struct {
	Transactions *walletrecords.Transactions
	Webhooks     *walletrecords.Webhooks
}

// APIScope limits a token that lists permissions to those names. A blank
// permissions store keeps today's access. A missing permission is 403
// forbidden: S3.4.6 does not name another code. The check reads the token
// APITokenAuth already stored. GET /transactions/{id} and
// PATCH /webhooks/{webhookId} resolve the resource first: a missing resource,
// and a resource that is not this account's, is 404 before the permission
// check. An id that is not a UUID is left to the handler, which answers 400.
func APIScope(lookups ScopeLookups, permission string) http.Middleware {
	if lookups.Transactions == nil || lookups.Webhooks == nil {
		panic("api scope: transactions and webhooks are required")
	}
	return func(ctx http.Context) {
		token, ok := requestctx.APIToken(ctx)
		if !ok || token == nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
			return
		}
		switch gateExternalChild(ctx, lookups) {
		case childPass:
			ctx.Request().Next()
			return
		case childAnswered:
			return
		}
		if !policies.APITokenAllows(token.Permissions, permission) {
			_ = responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, responses.CodeForbidden).Abort()
			return
		}
		ctx.Request().Next()
	}
}

// gateExternalChild resolves the external path child before the scope check.
// A transaction or webhook that is missing or belongs to another account is
// 404 here, because the transaction handler loads by id alone.
func gateExternalChild(ctx http.Context, lookups ScopeLookups) childGate {
	if raw := strings.TrimSpace(ctx.Request().Route("webhookId")); raw != "" {
		return gateExternalWebhook(ctx, lookups.Webhooks, raw)
	}
	if raw := strings.TrimSpace(ctx.Request().Route("id")); raw != "" {
		return gateExternalTransaction(ctx, lookups.Transactions, raw)
	}
	return childCheck
}

func gateExternalTransaction(ctx http.Context, transactions *walletrecords.Transactions, raw string) childGate {
	id, err := uuid.Parse(raw)
	if err != nil {
		return childPass
	}
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
		return childAnswered
	}
	tx, err := transactions.FindByIDForAccount(ctx.Context(), id, accountID)
	if err != nil && !rowMissing(err) {
		_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to load transaction").Abort()
		return childAnswered
	}
	if rowMissing(err) || tx == nil || tx.ID == uuid.Nil {
		_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "transaction not found").Abort()
		return childAnswered
	}
	return childCheck
}

func gateExternalWebhook(ctx http.Context, webhooks *walletrecords.Webhooks, raw string) childGate {
	id, err := uuid.Parse(raw)
	if err != nil {
		return childPass
	}
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
		return childAnswered
	}
	row, err := webhooks.FindOwnership(ctx.Context(), id)
	if err != nil && !rowMissing(err) {
		_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to load webhook").Abort()
		return childAnswered
	}
	if rowMissing(err) || !webhookVisibleToAccount(row, accountID) {
		_ = responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "webhook not found").Abort()
		return childAnswered
	}
	return childCheck
}

func webhookVisibleToAccount(row *models.WebhookOwnership, accountID uuid.UUID) bool {
	if row == nil || row.ID == uuid.Nil || row.WalletID != nil {
		return false
	}
	if row.AccountID != nil && *row.AccountID != accountID {
		return false
	}
	return true
}
