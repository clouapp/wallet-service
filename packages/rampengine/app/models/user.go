package models

import (
	"strconv"
	"time"

	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// User represents a XIP client account.
type User struct {
	orm.Model
	orm.SoftDeletes
	Handle          string     `gorm:"unique;not null" json:"handle"`
	Email           string     `gorm:"unique;not null" json:"email"`
	Password        string     `gorm:"not null" json:"-"`
	EmailVerifiedAt *time.Time `gorm:"column:email_verified_at" json:"-"`
	// SessionsRevokedAt ends every session opened before it: a token issued
	// earlier, or in the same second, is refused however long it has left.
	// Null until first set.
	SessionsRevokedAt *time.Time `gorm:"column:sessions_revoked_at" json:"-"`
	// SuspendedAt is when an admin took the account away from its owner; null
	// while it is theirs. SuspensionReason is the operator's own note, shown to
	// operators only.
	SuspendedAt      *time.Time `gorm:"column:suspended_at" json:"-"`
	SuspensionReason *string    `gorm:"column:suspension_reason;size:500" json:"-"`
}

// The states an account is in, as the admin lists and filters them.
const (
	UserStatusActive    = "active"
	UserStatusSuspended = "suspended"
	UserStatusDeleted   = "deleted"
)

// Status is the account's state. Deletion outranks suspension: a deleted
// account is gone whatever it was before.
func (u *User) Status() string {
	switch {
	case u.DeletedAt.Valid:
		return UserStatusDeleted
	case u.IsSuspended():
		return UserStatusSuspended
	}
	return UserStatusActive
}

// IsSuspended reports whether an admin has taken the account away.
func (u *User) IsSuspended() bool { return u.SuspendedAt != nil }

// Suspend takes the account away from its owner at now, for reason, and ends
// every session opened before it. Suspending again rewrites the reason and ends
// the sessions opened since, but keeps when the suspension started.
func (u *User) Suspend(now time.Time, reason string) {
	if u.SuspendedAt == nil {
		suspended := now
		u.SuspendedAt = &suspended
	}
	u.SuspensionReason = &reason
	u.RevokeSessions(now)
}

// Reactivate gives the account back. The sessions suspension ended stay ended.
func (u *User) Reactivate() {
	u.SuspendedAt = nil
	u.SuspensionReason = nil
}

// RevokeSessions ends every session opened before now (see
// identity.RevocationStamp). now comes from carbon.Now, the clock a token's iat
// is read from, so the two compare on one clock, a test's frozen one included.
func (u *User) RevokeSessions(now time.Time) {
	stamp := identity.RevocationStamp(now)
	u.SessionsRevokedAt = &stamp
}

// AcceptsTokenIssuedAt reports whether a token issued at iat still speaks for
// this account (see identity.AcceptsTokenIssuedAt). None does while the account
// is suspended: this is the check middleware.Auth and the refresh both make.
func (u *User) AcceptsTokenIssuedAt(iat time.Time) bool {
	return !u.IsSuspended() && identity.AcceptsTokenIssuedAt(u.SessionsRevokedAt, iat)
}

// FlagScopeIdentifier lets a User be used directly as a feature-flag scope
// (see the featureflag package).
func (u *User) FlagScopeIdentifier() string {
	return UserFlagScope(u.ID).FlagScopeIdentifier()
}

// UserFlagScope is a user as a feature-flag scope, for a caller that holds the
// id alone: a webhook, or a service asked about a user id. It keys the same row
// as the *User it stands for.
type UserFlagScope uint

// FlagScopeIdentifier is "user:{id}" — the prefix keeps it disjoint from the
// admin scope space.
func (id UserFlagScope) FlagScopeIdentifier() string {
	return "user:" + strconv.FormatUint(uint64(id), 10)
}

// SubjectID implements identity.Subject.
func (u *User) SubjectID() uint { return u.ID }

// SubjectType implements identity.Subject.
func (u *User) SubjectType() string { return identity.SubjectTypeUser }

// SubjectEmail implements identity.Subject.
func (u *User) SubjectEmail() string { return u.Email }

// SubjectPasswordHash implements identity.Credentialed.
func (u *User) SubjectPasswordHash() string { return u.Password }
