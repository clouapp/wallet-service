package db

import (
	sqldb "github.com/goravel/framework/contracts/database/db"

	"github.com/macrowallets/waas/app/models"
)

// RequireRow reports ErrRepositoryNotFound when the statement matched nothing.
func RequireRow(res *sqldb.Result) error {
	if res == nil || res.RowsAffected == 0 {
		return models.ErrRepositoryNotFound
	}
	return nil
}
