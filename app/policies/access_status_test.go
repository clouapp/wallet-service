package policies_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

func TestUserMayHoldSession(t *testing.T) {
	require.True(t, policies.UserMayHoldSession(models.StatusActive))
	for _, status := range []string{models.UserStatusInvited, "suspended", "", "ACTIVE"} {
		require.False(t, policies.UserMayHoldSession(status), status)
	}
}

func TestAccountAllowsRequest_ActiveAccountAllowsEverything(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		require.True(t, policies.AccountAllowsRequest(models.StatusActive, method), method)
	}
}

func TestAccountAllowsRequest_NonActiveAccountIsReadOnly(t *testing.T) {
	for _, status := range []string{models.AccountStatusFrozen, models.AccountStatusArchived, "suspended", ""} {
		for _, method := range []string{"GET", "get", "HEAD", "OPTIONS"} {
			require.True(t, policies.AccountAllowsRequest(status, method), status+" "+method)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "", "TRACE"} {
			require.False(t, policies.AccountAllowsRequest(status, method), status+" "+method)
		}
	}
}
