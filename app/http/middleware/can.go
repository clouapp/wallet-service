package middleware

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	goravelerrors "github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// PermUsersRead is the account member list. Routes cannot import policies.
const PermUsersRead = policies.PermUsersRead

// PermUsersWrite is POST and DELETE /v1/accounts/{accountId}/users[/{userId}],
// PATCH /v1/accounts/{accountId}/users/{userId} (AccountUpdateMember), and
// creating an account invite. Routes cannot import policies.
const PermUsersWrite = policies.PermUsersWrite

// PermRolesRead is the account role catalog. Routes cannot import policies.
const PermRolesRead = policies.PermRolesRead

// PermAccountWrite is PATCH /v1/accounts/{accountId}. Routes cannot import policies.
const PermAccountWrite = policies.PermAccountWrite

// PermAccountLifecycle is POST /v1/accounts/{accountId}/freeze and /archive. Routes cannot import policies.
const PermAccountLifecycle = policies.PermAccountLifecycle

// PermTokensRead is GET /v1/accounts/{accountId}/tokens. Routes cannot import policies.
const PermTokensRead = policies.PermTokensRead

// PermTokensWrite is POST /v1/accounts/{accountId}/tokens and DELETE /v1/accounts/{accountId}/tokens/{tokenId}.
// POST also runs MintAPITokenPermissions before the handler. Routes cannot import policies.
const PermTokensWrite = policies.PermTokensWrite

// Can refuses the route unless the account role already stored by
// AccountContext or AccountHeader holds permission. The Gate's ability for
// permission decides with policies.Can on the stored request grants, or on
// that role's code catalog. An unknown role fails closed, and a permission
// the Gate does not define is refused when the route is built. A child
// resource (member, token, or invite) is resolved first: a missing one is
// left to the handler, which answers 404, and only a resource that exists
// is 403 when the permission is missing.
func Can(accounts *accountsvc.Service, permission string) http.Middleware {
	if accounts == nil {
		panic("can: the account service is required")
	}
	return authorize(facades.Gate(), permission, accountChildSubject(accounts))
}

// accountChildSubject resolves the member, token or invite the path names,
// then hands the Gate the caller's account grants.
func accountChildSubject(accounts *accountsvc.Service) subject {
	return func(ctx http.Context) (map[string]any, outcome) {
		switch gateAccountChild(ctx, accounts) {
		case childPass:
			return nil, pass
		case childAnswered:
			return nil, answered
		}
		return map[string]any{policies.ArgGrants: accountGrants(ctx)}, decide
	}
}

type childGate int

const (
	childCheck childGate = iota
	childPass
	childAnswered
)

// gateAccountChild resolves a path child before the permission check. Routes
// with no child id keep today's check. An id that is not a UUID is passed
// through so the handler can answer 400. A missing child is passed through
// so the handler can answer 404. A failed read is 503 and does not admit.
func gateAccountChild(ctx http.Context, accounts *accountsvc.Service) childGate {
	switch {
	case strings.TrimSpace(ctx.Request().Route("userId")) != "":
		return gateMember(ctx, accounts)
	case strings.TrimSpace(ctx.Request().Route("tokenId")) != "":
		return gateToken(ctx, accounts)
	case strings.TrimSpace(ctx.Request().Route("id")) != "":
		return gateInvite(ctx, accounts)
	default:
		return childCheck
	}
}

func gateMember(ctx http.Context, accounts *accountsvc.Service) childGate {
	id, ok := childUUID(ctx, "userId")
	if !ok {
		return childPass
	}
	accountID, ok := gatedAccountID(ctx)
	if !ok {
		return childCheck
	}
	member, err := accounts.FindMember(ctx.Context(), accountID, id)
	if err != nil && !rowMissing(err) {
		abortWithJSON(ctx, http.StatusServiceUnavailable, http.Json{"error": "failed to load membership"})
		return childAnswered
	}
	if rowMissing(err) || member == nil || member.ID == uuid.Nil {
		return childPass
	}
	return childCheck
}

func gateToken(ctx http.Context, accounts *accountsvc.Service) childGate {
	id, ok := childUUID(ctx, "tokenId")
	if !ok {
		return childPass
	}
	accountID, ok := gatedAccountID(ctx)
	if !ok {
		return childCheck
	}
	token, err := accounts.FindAccessToken(ctx.Context(), id, accountID)
	if err != nil && !rowMissing(err) {
		abortWithJSON(ctx, http.StatusServiceUnavailable, http.Json{"error": "failed to load token"})
		return childAnswered
	}
	if rowMissing(err) || token == nil || token.ID == uuid.Nil {
		return childPass
	}
	return childCheck
}

func gateInvite(ctx http.Context, accounts *accountsvc.Service) childGate {
	id, ok := childUUID(ctx, "id")
	if !ok {
		return childPass
	}
	accountID, ok := gatedAccountID(ctx)
	if !ok {
		return childCheck
	}
	invite, err := accounts.FindOpenInvite(ctx.Context(), accountID, id)
	if err != nil && !rowMissing(err) {
		abortWithJSON(ctx, http.StatusServiceUnavailable, http.Json{"error": "failed to load invite"})
		return childAnswered
	}
	if rowMissing(err) || invite == nil || invite.ID == uuid.Nil {
		return childPass
	}
	return childCheck
}

// gatedAccountID is the account the child belongs to. AccountHeader and the
// API token store its id; AccountContext, which fronts the dashboard routes,
// stores only the account, so fall back to that one.
func gatedAccountID(ctx http.Context) (uuid.UUID, bool) {
	if id, ok := requestctx.AccountID(ctx); ok && id != uuid.Nil {
		return id, true
	}
	if account := AccountFrom(ctx); account != nil && account.ID != uuid.Nil {
		return account.ID, true
	}
	return uuid.Nil, false
}

func childUUID(ctx http.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(ctx.Request().Route(name)))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

func rowMissing(err error) bool {
	return errors.Is(err, models.ErrRepositoryNotFound) || errors.Is(err, goravelerrors.OrmRecordNotFound)
}

// accountGrants is the account catalog AccountContext or AccountHeader
// stored for this request, or the stored role's catalog when none was.
func accountGrants(ctx http.Context) policies.Grants {
	grants, ok := policies.AccountGrants(ctx)
	if !ok {
		grants = policies.AccountRoleGrants(AccountRole(ctx))
	}
	return grants
}
