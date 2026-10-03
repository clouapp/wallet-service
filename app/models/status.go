package models

// Status values shared by users, account and wallet memberships, and
// accounts. Anything other than StatusActive (invited, suspended, frozen,
// archived) restricts access; app/policies holds the rules.
const (
	StatusActive = "active"

	UserStatusInvited = "invited"

	AccountStatusFrozen   = "frozen"
	AccountStatusArchived = "archived"
)
