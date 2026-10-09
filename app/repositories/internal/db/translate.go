package db

import (
	"errors"
	"fmt"

	sqldb "github.com/goravel/framework/contracts/database/db"
	goravelerrors "github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/app/models"
)

// RequireRow reports ErrRepositoryNotFound when the statement matched nothing.
func RequireRow(res *sqldb.Result) error {
	if res == nil || res.RowsAffected == 0 {
		return models.ErrRepositoryNotFound
	}
	return nil
}

// LookupError translates the error of a FirstOrFail into the repository
// result: a missing row is the bare models.ErrRepositoryNotFound, anything
// else is wrapped with op.
func LookupError(err error, op string) error {
	if errors.Is(err, goravelerrors.OrmRecordNotFound) {
		return models.ErrRepositoryNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}
