package accounts

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/apitoken"
)

// memberRefusals are the account service's refusals of a membership change.
// Each answers 403 with its own sentence.
var memberRefusals = []error{
	accountsvc.ErrSelfMembership,
	accountsvc.ErrManageMembers,
	accountsvc.ErrGrantRole,
	accountsvc.ErrActOnMember,
	accountsvc.ErrLastOwner,
}

// mapError answers an error of the account services on the dashboard account
// routes. A sentinel keeps the status and the words the route has always
// answered. Anything else is logged and answered 500 with failure, the
// sentence naming what the handler was doing ("failed to create token").
func mapError(ctx http.Context, err error, failure string) http.Response {
	for _, refusal := range memberRefusals {
		if errors.Is(err, refusal) {
			return responses.FailMessage(ctx, http.StatusForbidden, refusal.Error())
		}
	}
	switch {
	case errors.Is(err, accountsvc.ErrAccountNotCreated):
		return responses.InternalError(ctx, err)
	case errors.Is(err, accountsvc.ErrMemberNotFound):
		return responses.FailMessage(ctx, http.StatusNotFound, accountsvc.ErrMemberNotFound.Error())
	case errors.Is(err, accountsvc.ErrMemberRole):
		return responses.FieldsFailed(ctx, map[string][]string{"role": {"role must be owner, admin, auditor or user"}})
	case errors.Is(err, accountsvc.ErrMemberStatus):
		return responses.FieldsFailed(ctx, map[string][]string{"status": {"status must be active or suspended"}})
	case errors.Is(err, accountsvc.ErrMemberChangeEmpty):
		return responses.FieldsFailed(ctx, map[string][]string{"role": {accountsvc.ErrMemberChangeEmpty.Error()}})
	case errors.Is(err, accountsvc.ErrUserLookup):
		return internalError(ctx, err, "failed to look up user")
	case errors.Is(err, accountsvc.ErrMemberNotAdded):
		return internalError(ctx, err, "failed to add user")
	case errors.Is(err, accountsvc.ErrMemberUnreadable):
		appfacades.Log().WithContext(ctx).Errorf("account: find membership after add: %v", err)
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "not a member of this account")
	case errors.Is(err, accountsvc.ErrFrontendURLRequired):
		appfacades.Log().WithContext(ctx).Errorf("account: invite link base is not configured")
		return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
	case errors.Is(err, accountsvc.ErrInviteInvalid):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrInviteInvalid.Error())
	case errors.Is(err, accountsvc.ErrInviteLoginRequired):
		return responses.Error(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, accountsvc.ErrInviteLoginRequired.Error())
	case errors.Is(err, accountsvc.ErrInvitePassword):
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, accountsvc.ErrInvitePassword.Error())
	case errors.Is(err, accountsvc.ErrAccessTokenNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "token not found")
	case errors.Is(err, apitoken.ErrSign):
		return internalError(ctx, err, "failed to sign token")
	default:
		return internalError(ctx, err, failure)
	}
}

// mapInviteError is mapError for the routes that take an email (add a member,
// invite one). A role the caller cannot grant is the fixed forbidden envelope,
// not the sentinel's sentence: the refusal can wrap what the customer typed,
// so the log keeps only its type.
func mapInviteError(ctx http.Context, err error, failure string) http.Response {
	if errors.Is(err, accountsvc.ErrGrantRole) {
		slog.Error("account invite grant refused", "error_type", fmt.Sprintf("%T", err))
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, "forbidden")
	}
	return mapError(ctx, err, failure)
}

// mapAcceptError is mapError for accepting an invite: a role the inviter can
// no longer grant is 422 with the sentinel's sentence, because the caller is
// not the one granting it.
func mapAcceptError(ctx http.Context, err error) http.Response {
	if errors.Is(err, accountsvc.ErrGrantRole) {
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, accountsvc.ErrGrantRole.Error())
	}
	return mapError(ctx, err, "failed to accept invite")
}

// mapPreviewError answers every failure of an invite preview with the same
// 404, so the answer says nothing about why the token cannot be used. A
// failure other than an invalid invite is logged.
func mapPreviewError(ctx http.Context, err error) http.Response {
	if !errors.Is(err, accountsvc.ErrInviteInvalid) {
		slog.Error("controller internal error", "endpoint", "preview invite", "error", err)
	}
	return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, accountsvc.ErrInviteInvalid.Error())
}

// internalError logs err and answers 500 with failure. A sentence gets the
// internal code and a machine code (internal_error) is its own code, as the
// routes have always answered.
func internalError(ctx http.Context, err error, failure string) http.Response {
	slog.Error("controller internal error", "endpoint", failure, "error", err)
	return responses.FailMessage(ctx, http.StatusInternalServerError, failure)
}
