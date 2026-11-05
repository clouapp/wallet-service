package contracts

// FlagScope lets a domain type identify itself as a feature-flag scope, so a
// *User, *Team, etc. can be passed directly where a scope is expected.
type FlagScope interface {
	FlagScopeIdentifier() string
}
