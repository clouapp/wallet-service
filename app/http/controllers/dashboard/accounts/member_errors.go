package accounts

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func mapMemberError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, accountsvc.ErrMemberNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": accountsvc.ErrMemberNotFound.Error()})
	case errors.Is(err, accountsvc.ErrMemberRole):
		return memberFieldError(ctx, "role", "role must be owner, admin, auditor or user")
	case errors.Is(err, accountsvc.ErrMemberStatus):
		return memberFieldError(ctx, "status", "status must be active or suspended")
	case errors.Is(err, accountsvc.ErrMemberChangeEmpty):
		return memberFieldError(ctx, "role", accountsvc.ErrMemberChangeEmpty.Error())
	case errors.Is(err, accountsvc.ErrSelfMembership):
		return memberForbidden(ctx, accountsvc.ErrSelfMembership)
	case errors.Is(err, accountsvc.ErrManageMembers):
		return memberForbidden(ctx, accountsvc.ErrManageMembers)
	case errors.Is(err, accountsvc.ErrGrantRole):
		return memberForbidden(ctx, accountsvc.ErrGrantRole)
	case errors.Is(err, accountsvc.ErrActOnMember):
		return memberForbidden(ctx, accountsvc.ErrActOnMember)
	case errors.Is(err, accountsvc.ErrLastOwner):
		return memberForbidden(ctx, accountsvc.ErrLastOwner)
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}

func memberForbidden(ctx http.Context, sentinel error) http.Response {
	return responses.Send(ctx, http.StatusForbidden, http.Json{"error": sentinel.Error()})
}

func memberFieldError(ctx http.Context, field, message string) http.Response {
	return responses.FieldsFailed(ctx, map[string][]string{
		field: {message},
	})
}
