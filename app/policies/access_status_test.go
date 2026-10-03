package policies

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestUserMayHoldSession(t *testing.T) {
	require.True(t, UserMayHoldSession(models.StatusActive))
	for _, status := range []string{models.UserStatusInvited, "suspended", "", "ACTIVE"} {
		require.False(t, UserMayHoldSession(status), status)
	}
}

func TestAccountAllowsRequest_ActiveAccountAllowsEverything(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		require.True(t, AccountAllowsRequest(models.StatusActive, method), method)
	}
}

func TestAccountAllowsRequest_NonActiveAccountIsReadOnly(t *testing.T) {
	for _, status := range []string{models.AccountStatusFrozen, models.AccountStatusArchived, "suspended", ""} {
		for _, method := range []string{"GET", "get", "HEAD", "OPTIONS"} {
			require.True(t, AccountAllowsRequest(status, method), status+" "+method)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "", "TRACE"} {
			require.False(t, AccountAllowsRequest(status, method), status+" "+method)
		}
	}
}
