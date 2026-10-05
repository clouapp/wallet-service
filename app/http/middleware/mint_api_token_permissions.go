package middleware

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/policies"
)

// MintAPITokenPermissions is policies.MintAPITokenPermissions on
// POST /v1/accounts/{accountId}/tokens, after Can(tokens.write) and before
// the handler. Every named permission must be one the account role holds.
// Owner and admin hold the API token catalog. User holds wallets.read,
// addresses.create, withdrawals.create, sweep.execute, and webhooks.read.
// Auditor holds wallets.read, webhooks.read, and transactions.read.
// A name outside that catalog is left for the handler, which answers 422.
// A denial is 403 with the policy message and does not create the token.
func MintAPITokenPermissions() http.Middleware {
	return func(ctx http.Context) {
		permissions, apply := readMintAPITokenPermissions(ctx)
		if apply {
			decision := policies.MintAPITokenPermissions(AccountRole(ctx), permissions)
			if !decision.Allowed() {
				abortWithJSON(ctx, http.StatusForbidden, http.Json{"error": decision.Message()})
				return
			}
		}
		ctx.Request().Next()
	}
}

// readMintAPITokenPermissions restores the body so the handler can validate it.
// apply is false when the body is not a JSON object, permissions is not a
// string array, or a name is outside the catalog. Those stay HTTP 422.
func readMintAPITokenPermissions(ctx http.Context) ([]string, bool) {
	if ctx == nil || ctx.Request() == nil {
		return nil, false
	}
	origin := ctx.Request().Origin()
	if origin == nil || origin.Body == nil {
		return nil, false
	}
	body, err := io.ReadAll(origin.Body)
	_ = origin.Body.Close()
	origin.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	return mintAPITokenPermissions(body)
}

// mintAPITokenPermissions reports the permissions array the policy can decide.
// A missing permissions field and JSON null are an empty grant. Anything that
// is not a catalog subset returns apply false so validation still answers 422.
func mintAPITokenPermissions(body []byte) ([]string, bool) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return nil, false
	}
	var payload struct {
		Permissions json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	if len(payload.Permissions) == 0 || string(payload.Permissions) == "null" {
		return nil, true
	}
	var permissions []string
	if err := json.Unmarshal(payload.Permissions, &permissions); err != nil {
		return nil, false
	}
	for _, permission := range permissions {
		if !policies.IsAPITokenPermission(permission) {
			return nil, false
		}
	}
	return permissions, true
}
