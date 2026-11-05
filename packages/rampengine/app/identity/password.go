package identity

import (
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// ErrCurrentPasswordIncorrect is returned when the current password supplied
// alongside a password change does not match the stored hash.
var ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")

// RehashPassword verifies the subject's current password and returns the hash
// of the new one. It does not persist anything — which repository owns the
// subject is the caller's business, and the only part worth sharing is the
// check.
//
// Re-proving the current password is what keeps a stolen session from being
// enough to take an account over: the attacker holds a token, not the secret.
// The check runs before the hash so a wrong attempt costs an attacker the
// comparison and nothing more.
func RehashPassword(subject Credentialed, current, next string) (string, error) {
	if !facades.Hash().Check(current, subject.SubjectPasswordHash()) {
		return "", ErrCurrentPasswordIncorrect
	}
	hashed, err := facades.Hash().Make(next)
	if err != nil {
		return "", fmt.Errorf("identity: hash password: %w", err)
	}
	return hashed, nil
}
