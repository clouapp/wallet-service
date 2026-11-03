package customers

import "github.com/macrowallets/waas/packages/rampengine/app/repositories"

// ErrCustomerClosed refuses moving money or KYC data for a closed customer
// (API contract §1). Its reads keep answering: a closed company stays listed
// and readable, so the app keeps its history instead of forgetting it. It is
// the repositories' own sentinel, so a reservation a close outran answers the
// same customer_closed.
var ErrCustomerClosed = repositories.ErrCustomerClosed
