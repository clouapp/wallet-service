package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
	"github.com/goravel/framework/facades"
)

type DBExists struct{}

func (r *DBExists) Signature() string {
	return "db_exists"
}

func (r *DBExists) Passes(_ context.Context, _ validation.Data, val any, options ...any) bool {
	if len(options) < 2 {
		return true
	}
	table, _ := options[0].(string)
	column, _ := options[1].(string)
	if table == "" || column == "" {
		return true
	}

	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}

	count, err := facades.Orm().Query().Table(table).Where(column+" = ?", s).Count()
	if err != nil {
		return true
	}
	return count > 0
}

func (r *DBExists) Message(_ context.Context) string {
	return "The selected :attribute is invalid."
}
