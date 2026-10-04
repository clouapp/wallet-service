package policies

import (
	"context"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// accountMemberships is the membership read account policies use.
// The concrete repository is installed once from the composition root.
type accountMemberships interface {
	FindByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error)
}

var accountUsers accountMemberships

// BindAccountUsers installs the membership lookup used by account policies.
func BindAccountUsers(repo accountMemberships) {
	if repo == nil {
		panic("policies: account users repository is required")
	}
	accountUsers = repo
}

func accountUserRepository() accountMemberships {
	if accountUsers == nil {
		panic("policies: account users repository is not bound")
	}
	return accountUsers
}
