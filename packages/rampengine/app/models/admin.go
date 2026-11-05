package models

import (
	"strconv"
	"time"

	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// AdminMorphType is the canonical model_type string used by the RBAC
// polymorphic tables (model_has_roles, model_has_permissions). It is an alias
// for the subject type so the RBAC pivots and the OTP/MFA tables can never
// disagree about what an admin is called.
const AdminMorphType = identity.SubjectTypeAdmin

// Admin is a back-office user with elevated privileges. Admins authenticate
// via a separate JWT guard (`admin`) and are governed by RBAC roles and
// permissions. Membership in the `super_admin` role grants every permission.
type Admin struct {
	orm.Model
	orm.SoftDeletes
	Name            string     `gorm:"not null" json:"name"`
	Email           string     `gorm:"unique;not null" json:"email"`
	Password        string     `gorm:"not null" json:"-"`
	EmailVerifiedAt *time.Time `gorm:"column:email_verified_at" json:"-"`
	LastLoginAt     *time.Time `gorm:"column:last_login_at" json:"last_login_at,omitempty"`
	// SessionsRevokedAt ends every session opened before it: a token issued
	// earlier, or in the same second, is refused however long it has left.
	// Null until first set.
	SessionsRevokedAt *time.Time `gorm:"column:sessions_revoked_at" json:"-"`
}

// RevokeSessions ends every session opened before now (see
// identity.RevocationStamp). now comes from carbon.Now, the clock a token's iat
// is read from, so the two compare on one clock, a test's frozen one included.
func (a *Admin) RevokeSessions(now time.Time) {
	stamp := identity.RevocationStamp(now)
	a.SessionsRevokedAt = &stamp
}

// AcceptsTokenIssuedAt reports whether a token issued at iat still speaks for
// this admin (see identity.AcceptsTokenIssuedAt).
func (a *Admin) AcceptsTokenIssuedAt(iat time.Time) bool {
	return identity.AcceptsTokenIssuedAt(a.SessionsRevokedAt, iat)
}

// FlagScopeIdentifier lets an Admin be used directly as a feature-flag scope
// (see the featureflag package). The key is "admin:{id}".
func (a *Admin) FlagScopeIdentifier() string {
	return "admin:" + strconv.FormatUint(uint64(a.ID), 10)
}

// SubjectID implements identity.Subject.
func (a *Admin) SubjectID() uint { return a.ID }

// SubjectType implements identity.Subject.
func (a *Admin) SubjectType() string { return identity.SubjectTypeAdmin }

// SubjectEmail implements identity.Subject.
func (a *Admin) SubjectEmail() string { return a.Email }

// SubjectPasswordHash implements identity.Credentialed.
func (a *Admin) SubjectPasswordHash() string { return a.Password }
