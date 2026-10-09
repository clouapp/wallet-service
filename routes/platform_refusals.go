package routes

import (
	accountsvc "github.com/macrowallets/waas/app/services/account"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// platformRefusals is the sentence each /v1/platform route answers a caller who
// is not a platform admin: the one its service returns. The keys are the
// registered method and pattern.
var platformRefusals = map[string]error{
	"GET /v1/platform/features":                        featuressvc.ErrPlatformForbidden,
	"PATCH /v1/platform/features/{key}":                featuressvc.ErrPlatformForbidden,
	"GET /v1/platform/features/{scope}/{id}":           featuressvc.ErrPlatformForbidden,
	"PUT /v1/platform/features/{scope}/{id}/{feature}": featuressvc.ErrPlatformForbidden,
	"PUT /v1/platform/features/{scope}/{id}":           featuressvc.ErrPlatformForbidden,

	"PATCH /v1/platform/chains/{chainId}/rpc": chainsvc.ErrPlatformForbidden,
	"PATCH /v1/platform/chains/{chainId}":     chainsvc.ErrPlatformForbidden,

	"POST /v1/platform/settings/mail/test":                   settingssvc.ErrPlatformForbidden,
	"GET /v1/platform/settings":                              settingssvc.ErrPlatformViewForbidden,
	"GET /v1/platform/settings/{group}":                      settingssvc.ErrPlatformViewForbidden,
	"PUT /v1/platform/settings/{group}":                      settingssvc.ErrPlatformForbidden,
	"POST /v1/platform/settings/sections/{section}/cache":    settingssvc.ErrPlatformForbidden,
	"POST /v1/platform/settings/sections/{section}/reset":    settingssvc.ErrPlatformForbidden,
	"GET /v1/platform/accounts/{accountId}/settings/{group}": settingssvc.ErrPlatformViewForbidden,
	"PUT /v1/platform/accounts/{accountId}/settings/{group}": settingssvc.ErrPlatformForbidden,

	"GET /v1/platform/accounts":                       accountsvc.ErrPlatformViewForbidden,
	"POST /v1/platform/accounts/{accountId}/freeze":   accountsvc.ErrPlatformLifecycleForbidden,
	"POST /v1/platform/accounts/{accountId}/unfreeze": accountsvc.ErrPlatformLifecycleForbidden,
	"POST /v1/platform/accounts/{accountId}/archive":  accountsvc.ErrPlatformLifecycleForbidden,
	"GET /v1/platform/accounts/{accountId}/users":     accountsvc.ErrPlatformAccountUsersForbidden,
	"POST /v1/platform/accounts/{accountId}/owners":   accountsvc.ErrPlatformOwnersForbidden,

	"GET /v1/platform/activity": activitysvc.ErrPlatformForbidden,

	"GET /v1/platform/users":                       usersvc.ErrViewForbidden,
	"POST /v1/platform/users/{id}/suspend":         usersvc.ErrPlatformForbidden,
	"POST /v1/platform/users/{id}/reactivate":      usersvc.ErrPlatformForbidden,
	"POST /v1/platform/users/{id}/sessions/revoke": usersvc.ErrSessionsForbidden,
	"DELETE /v1/platform/users/{id}/mfa":           usersvc.ErrMFAForbidden,
}

// platformRefusal is middleware.PlatformAdmin's refusal func.
func platformRefusal(method, pattern string) string {
	if err, ok := platformRefusals[method+" "+pattern]; ok {
		return err.Error()
	}
	return ""
}
