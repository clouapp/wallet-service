package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// Unique reports whether no row stores this value yet. A rule without its table
// and column fails every value. A failed read passes. The table's unique
// constraint still rejects the write.
type Unique struct {
	rows RowCount
}

// NewUnique checks uniqueness through rows.
func NewUnique(rows RowCount) *Unique {
	if rows == nil {
		panic("unique rule: row count is required")
	}
	return &Unique{rows: rows}
}

func (r *Unique) Signature() string {
	return "unique"
}

func (r *Unique) Passes(ctx context.Context, _ validation.Data, val any, options ...any) bool {
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
	return count == 0
}

func (r *Unique) Message(_ context.Context) string {
	return "The :attribute has already been taken."
}
