package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// DBExists reports whether a row is already stored. A rule without its table
// and column fails every value. A failed read passes, so a blank value is left
// to required and a database error is not turned into a false rejection.
type DBExists struct {
	rows RowCount
}

// NewDBExists checks existence through rows.
func NewDBExists(rows RowCount) *DBExists {
	if rows == nil {
		panic("db_exists rule: row count is required")
	}
	return &DBExists{rows: rows}
}

func (r *DBExists) Signature() string {
	return "db_exists"
}

func (r *DBExists) Passes(ctx context.Context, _ validation.Data, val any, options ...any) bool {
	table, column, ok := ruleColumn(options)
	if !ok {
		return false
	}
	value, ok := ruleString(val)
	if !ok {
		return true
	}
	count, err := r.rows.CountEquals(ctx, table, column, value)
	if err != nil {
		return true
	}
	return count > 0
}

func (r *DBExists) Message(_ context.Context) string {
	return "The selected :attribute is invalid."
}
