// Package pgerr reads Postgres error conditions out of ORM errors.
package pgerr

import "strings"

// IsUniqueViolation reports whether err is Postgres refusing a duplicate key
// (SQLSTATE 23505). The ORM exposes SQLSTATE only through the message, so the
// check stays on that text.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
}
