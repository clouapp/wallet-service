package rules

import "context"

// RowCount is the existence read db_exists and unique are allowed to make.
// The rule asks this port and never the ORM. A write is still settled by the
// table constraint.
type RowCount interface {
	CountEquals(ctx context.Context, table, column, value string) (int64, error)
}

func ruleColumn(options []any) (table, column string, ok bool) {
	if len(options) < 2 {
		return "", "", false
	}
	table, _ = options[0].(string)
	column, _ = options[1].(string)
	if table == "" || column == "" {
		return "", "", false
	}
	return table, column, true
}

func ruleString(val any) (string, bool) {
	text, ok := val.(string)
	if !ok || text == "" {
		return "", false
	}
	return text, true
}
