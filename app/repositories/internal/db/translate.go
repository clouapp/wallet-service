package db

import (
	"fmt"
	"strings"

	sqldb "github.com/goravel/framework/contracts/database/db"
	goravelerrors "github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/app/models"
)

// NotFound maps a missing row to ErrRepositoryNotFound and wraps any other
// error with op.
func NotFound(err error, op string) error {
	if err == nil {
		return nil
	}
	if goravelerrors.Is(err, goravelerrors.OrmRecordNotFound) {
		return models.ErrRepositoryNotFound
	}
	if op == "" {
		return err
	}
	return fmt.Errorf("%s: %w", op, err)
}

// RequireRow reports ErrRepositoryNotFound when the statement matched nothing.
func RequireRow(res *sqldb.Result) error {
	if res == nil || res.RowsAffected == 0 {
		return models.ErrRepositoryNotFound
	}
	return nil
}

// IsUniqueViolation reports whether err is Postgres refusing a duplicate key.
// The ORM exposes SQLSTATE through the message, so the check stays on that text.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
}
