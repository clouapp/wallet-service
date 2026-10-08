package policies

const (
	rankOwner  = 3
	rankAdmin  = 2
	rankMember = 1
)

// AccountRoleRank is the ladder owner (3) > admin (2) > auditor = user (1).
// An unknown role has no rank.
func AccountRoleRank(role string) (int, bool) {
	switch role {
	case roleOwner:
		return rankOwner, true
	case roleAdmin:
		return rankAdmin, true
	case roleAuditor, roleUser:
		return rankMember, true
	default:
		return 0, false
	}
}

// KnownAccountRole reports whether role is one of owner, admin, auditor, user.
func KnownAccountRole(role string) bool {
	_, ok := AccountRoleRank(role)
	return ok
}

// IsAccountOwner reports whether role is the owner rank.
func IsAccountOwner(role string) bool {
	return role == roleOwner
}

// AccountOwnerRole is the stored owner role. Callers pass it to queries; they
// do not compare role strings themselves.
func AccountOwnerRole() string {
	return roleOwner
}

// ManagesMembers reports whether the role may change other members.
// Owner and admin may. Auditor and user may not, even though they share a rank.
func ManagesMembers(role string) bool {
	rank, ok := AccountRoleRank(role)
	return ok && rank >= rankAdmin
}

// MayGrant reports whether actorRole may assign grantedRole.
// A role above the actor's rank is refused. An equal rank is allowed.
func MayGrant(actorRole, grantedRole string) bool {
	actor, actorOK := AccountRoleRank(actorRole)
	granted, grantedOK := AccountRoleRank(grantedRole)
	if !actorOK || !grantedOK {
		return false
	}
	return granted <= actor
}

// MayActOn reports whether actorRole may change a member who currently holds targetRole.
// A target above the actor's rank is refused. An equal rank is allowed.
func MayActOn(actorRole, targetRole string) bool {
	actor, actorOK := AccountRoleRank(actorRole)
	target, targetOK := AccountRoleRank(targetRole)
	if !actorOK || !targetOK {
		return false
	}
	return target <= actor
}
