package repositories

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// Existence is the row count db_exists and unique ask for. The validation
// rule does not call the ORM.
type Existence struct {
	db.Base
}

// NewExistence wraps an orm.Query. Pass nil for a fresh query per call.
func NewExistence(query orm.Query) *Existence {
	return &Existence{Base: db.NewBase(query)}
}

// CountEquals returns how many rows have column equal to value.
func (e *Existence) CountEquals(ctx context.Context, table, column, value string) (int64, error) {
	if e == nil {
		return 0, fmt.Errorf("count equals: repository is required")
	}
	if table == "" || column == "" {
		return 0, fmt.Errorf("count equals: table and column are required")
	}
	count, err := e.Query(ctx).Table(table).Where(column+" = ?", value).Count()
	if err != nil {
		return 0, fmt.Errorf("count equals: %w", err)
	}
	return count, nil
}
