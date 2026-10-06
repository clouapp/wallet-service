package policies

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestUser_May_HoldSession(t *testing.T) {
	require.True(t, UserMayHoldSession(models.StatusActive))
	for _, status := range []string{models.UserStatusInvited, "suspended", "", "ACTIVE"} {
		require.False(t, UserMayHoldSession(status), status)
	}
}

func TestUser_Is_Suspended(t *testing.T) {
	require.False(t, UserIsSuspended(nil))
	zero := time.Time{}
	require.False(t, UserIsSuspended(&zero))
	at := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	require.True(t, UserIsSuspended(&at))
}

func TestAccount_AllowsRequest_ActiveAccountAllowsEverything(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		require.True(t, AccountAllowsRequest(models.StatusActive, method), method)
	}
}

func TestAccount_AllowsRequest_NonActiveAccountIsReadOnly(t *testing.T) {
	for _, status := range []string{models.AccountStatusFrozen, models.AccountStatusArchived, "suspended", ""} {
		for _, method := range []string{"GET", "get", "HEAD", "OPTIONS"} {
			require.True(t, AccountAllowsRequest(status, method), status+" "+method)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "", "TRACE"} {
			require.False(t, AccountAllowsRequest(status, method), status+" "+method)
		}
	}
}
