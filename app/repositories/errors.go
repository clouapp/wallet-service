package repositories

import "strings"

// IsUniqueViolation reports whether a write was rejected by a unique constraint
// (PostgreSQL SQLSTATE 23505), which the ORM only exposes through the message.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
}
