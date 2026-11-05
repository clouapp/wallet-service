package repositories

import "strings"

// sortable is one listing's whitelist of sort keys, each mapped to the column
// it orders by, and the order it uses when the request names none.
type sortable struct {
	columns     map[string]string
	column, dir string
}

// parse reads "key" as ascending and "-key" as descending. A key outside the
// whitelist keeps its direction but orders by the default column, so request
// input never reaches ORDER BY.
func (s sortable) parse(raw string) (string, string) {
	if raw == "" {
		return s.column, s.dir
	}
	key, desc := strings.CutPrefix(raw, "-")
	dir := "asc"
	if desc {
		dir = "desc"
	}
	column, ok := s.columns[key]
	if !ok {
		column = s.column
	}
	return column, dir
}
