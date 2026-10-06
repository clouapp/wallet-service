package policies

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/app/models"
)

func TestUser_May_HoldSession(t *testing.T) {
	assert.True(t, UserMayHoldSession(models.StatusActive))
	for _, status := range []string{models.UserStatusInvited, "suspended", "", "ACTIVE"} {
		assert.False(t, UserMayHoldSession(status), status)
	}
}

func TestUser_Is_Suspended(t *testing.T) {
	assert.False(t, UserIsSuspended(nil))
	zero := time.Time{}
	assert.False(t, UserIsSuspended(&zero))
	at := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	assert.True(t, UserIsSuspended(&at))
}

func TestAccount_AllowsRequest_ActiveAccountAllowsEverything(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		assert.True(t, AccountAllowsRequest(models.StatusActive, method), method)
	}
}

func TestAccount_AllowsRequest_NonActiveAccountIsReadOnly(t *testing.T) {
	for _, status := range []string{models.AccountStatusFrozen, models.AccountStatusArchived, "suspended", ""} {
		for _, method := range []string{"GET", "get", "HEAD", "OPTIONS"} {
			assert.True(t, AccountAllowsRequest(status, method), status+" "+method)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "", "TRACE"} {
			assert.False(t, AccountAllowsRequest(status, method), status+" "+method)
		}
	}
}
