package providers

import "github.com/macrowallets/waas/packages/rampengine/app/models"

// HolderDTO is the customer an operator's provider act is for, as its route
// named it (KYB contract §12.5): an account's individual customer on
// /users/{id}, the customer itself on /customers/{id}.
type HolderDTO struct {
	Customer *models.Customer
	// Owner is who acts for a business at a provider — its earliest owner;
	// nil for a person, whose own account the service reads where an act
	// needs it.
	Owner *models.User
} //	@name	AdminProviderHolderDTO
