package policies

import (
	"net/http"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
)

// UserMayHoldSession reports whether a user may sign in or keep using a
// session. Only active users may; invited users have no credentials yet and
// any other status (suspended) is a deliberate block.
func UserMayHoldSession(status string) bool {
	return status == models.StatusActive
}

// UserIsSuspended reports a platform suspension. Membership status is a
// different column and a different check.
func UserIsSuspended(suspendedAt *time.Time) bool {
	return suspendedAt != nil && !suspendedAt.IsZero()
}

// AccountAllowsRequest reports whether a request may act on an account in
// the given status. An active account allows everything; a frozen, archived
// or otherwise non-active account is read-only.
func AccountAllowsRequest(accountStatus, method string) bool {
	if accountStatus == models.StatusActive {
		return true
	}
	return isReadOnlyMethod(method)
}

func isReadOnlyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
