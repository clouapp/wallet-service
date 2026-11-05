package repositories

import (
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RBACSubject is what holds roles and permissions: an admin, or a customer's
// membership. It is narrower than identity.Subject on purpose — a membership
// does not authenticate and has no email to give, and identity.Subject keeps
// meaning "who logs in".
type RBACSubject interface {
	SubjectID() uint
	SubjectType() string
}

// rbacGuard is the guard a subject's roles and permissions live under. Roles
// are scoped by guard so that one permission name can mean one thing in the
// back office and another for a customer; deriving the guard from the subject
// means a caller can never resolve a check against another audience's catalog.
//
// A membership's guard is the customer guard, which is RBAC's alone; every
// other subject's is the auth guard it logs in under. An unknown subject maps
// to "" and holds nothing.
func rbacGuard(subject RBACSubject) string {
	if subject.SubjectType() == models.CustomerMemberMorphType {
		return models.CustomerGuard
	}
	return identity.GuardForSubjectType(subject.SubjectType())
}
