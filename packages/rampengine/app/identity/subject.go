// Package identity carries what admins and users have in common as
// authenticatable subjects.
//
// The two live in separate tables on purpose — a back-office account and a
// customer account are different blast radii, and a missing `WHERE type = …`
// should never be the only thing standing between them. What they share is
// everything *around* the account: one-time codes, MFA enrollment, roles. Those
// tables key off (subject_type, subject_id) rather than being written twice.
package identity

// Subject is an entity that can authenticate: today a User or an Admin.
//
// SubjectEmail rather than Email because the models carry an `Email` field, and
// Go does not allow a method to share its name with a field.
type Subject interface {
	SubjectID() uint
	SubjectType() string
	SubjectEmail() string
}

// Credentialed is a Subject whose password can be re-checked.
//
// It is separate from Subject because most things that key off a subject — a
// one-time code, a role assignment — have no business seeing a password hash.
// Only the flows that ask the subject to prove themselves again (disabling MFA,
// changing a password) need it, and they say so by asking for this instead.
type Credentialed interface {
	Subject
	SubjectPasswordHash() string
}

// The canonical subject_type values. They match the table names, which is what
// the RBAC pivots already assumed when they were written as (model_type,
// model_id).
const (
	SubjectTypeUser  = "users"
	SubjectTypeAdmin = "admins"
)

// SubjectTypes is every valid subject_type, in the order the CHECK constraints
// spell them. A polymorphic column cannot carry a foreign key, so this list is
// the only thing keeping a typo out of the column.
var SubjectTypes = []string{SubjectTypeUser, SubjectTypeAdmin}

// Auth guard names, matching the guards declared in config/auth.go.
const (
	GuardUser  = "user"
	GuardAdmin = "admin"
)

// GuardFor maps a subject to the auth guard it logs in under, which is also
// what keeps one audience's counters and codes apart from the other's
// ("admin:…" and "user:…" for the same id). The RBAC repositories derive a
// role's guard from it too, next to the one guard that is RBAC's alone (see
// repositories.RBACSubject).
//
// Deriving it from the subject rather than passing it alongside means a caller
// cannot pair an admin with the user guard by accident.
func GuardFor(subject Subject) string {
	return GuardForSubjectType(subject.SubjectType())
}

// GuardForSubjectType is GuardFor by subject type alone, for a caller holding
// no Subject. An unknown type maps to "".
func GuardForSubjectType(subjectType string) string {
	switch subjectType {
	case SubjectTypeAdmin:
		return GuardAdmin
	case SubjectTypeUser:
		return GuardUser
	default:
		return ""
	}
}

// OTPPurpose discriminates what a one-time code was issued for. A code is only
// ever redeemable for the purpose it was issued under: the flows have different
// preconditions, and sharing a namespace would let a public endpoint invalidate
// — or redeem — a code issued behind a password check.
type OTPPurpose string

const (
	// OTPPurposeEmailVerify confirms ownership of the address at registration.
	OTPPurposeEmailVerify OTPPurpose = "email_verify"

	// OTPPurposePasswordReset authorizes setting a new password.
	OTPPurposePasswordReset OTPPurpose = "password_reset"

	// OTPPurposeLoginOTP is step 2 of password login: issued only after the
	// password checked out, so it is a second factor.
	OTPPurposeLoginOTP OTPPurpose = "login_otp"

	// OTPPurposePasswordlessLogin is the email-only login flow. It is kept
	// disjoint from OTPPurposeLoginOTP because it has no first factor of its
	// own — the emailed code IS the credential, so it is the one that carries
	// attempt limits.
	OTPPurposePasswordlessLogin OTPPurpose = "passwordless_login"

	// OTPPurposeWalletRestore authorizes reactivating local wallets the user
	// already had on this device. Disjoint from login purposes so a public
	// login send cannot invalidate a restore code mid-flow.
	OTPPurposeWalletRestore OTPPurpose = "wallet_restore"
)
