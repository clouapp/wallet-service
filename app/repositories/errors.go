package repositories

import "github.com/macrowallets/waas/app/repositories/internal/db"

// IsUniqueViolation reports whether a write was rejected by a unique constraint
// (PostgreSQL SQLSTATE 23505), which the ORM only exposes through the message.
func IsUniqueViolation(err error) bool {
	return db.IsUniqueViolation(err)
}
