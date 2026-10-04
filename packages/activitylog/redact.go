package activitylog

import (
	"fmt"
	"strings"
)

// TextRedaction replaces one text column with "<column>Set" for rows whose
// MatchColumns equal one of Pairs. The before-image SELECT returns NULL for
// that text on a matching row and a boolean under "<column>Set", so the text
// is never handed to the process and never stored.
type TextRedaction struct {
	Column       string
	MatchColumns []string
	Pairs        [][]string
}

func (r TextRedaction) active() bool {
	return r.Column != "" && len(r.MatchColumns) > 0 && len(r.Pairs) > 0
}

func (r TextRedaction) validate() error {
	if r.Column == "" && len(r.MatchColumns) == 0 && len(r.Pairs) == 0 {
		return nil
	}
	if !r.active() {
		return fmt.Errorf("activitylog: text redaction needs a column, match columns, and pairs")
	}
	if !safeToken(r.Column) {
		return fmt.Errorf("activitylog: text redaction column %q is not a safe identifier", r.Column)
	}
	for _, column := range r.MatchColumns {
		if !safeToken(column) {
			return fmt.Errorf("activitylog: text redaction match column %q is not a safe identifier", column)
		}
	}
	width := len(r.MatchColumns)
	for _, pair := range r.Pairs {
		if len(pair) != width {
			return fmt.Errorf("activitylog: text redaction pair %q does not match %d columns", pair, width)
		}
		for _, part := range pair {
			if !safeToken(part) {
				return fmt.Errorf("activitylog: text redaction token %q is not safe", part)
			}
		}
	}
	return nil
}

func (r TextRedaction) matches(row map[string]any) bool {
	if !r.active() {
		return false
	}
	got := make([]string, len(r.MatchColumns))
	for i, column := range r.MatchColumns {
		got[i] = cellString(row[column])
	}
	for _, pair := range r.Pairs {
		if len(pair) != len(got) {
			continue
		}
		same := true
		for i := range pair {
			if pair[i] != got[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// projectedSelect renders the before-image column list. A redacted column is
// two CASE expressions: the text is NULL for a matching row, and "<column>Set"
// is the boolean. Placeholders are not used, so the WHERE arguments gorm
// already numbered stay valid.
func (t Table) projectedSelect(columns []string, quote func(string) string) (string, error) {
	if err := t.TextRedaction.validate(); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	parts := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		if seen[column] {
			continue
		}
		seen[column] = true
		if t.TextRedaction.active() && column == t.TextRedaction.Column {
			text, set, err := t.TextRedaction.expressions(quote)
			if err != nil {
				return "", err
			}
			parts = append(parts, text, set)
			continue
		}
		parts = append(parts, quote(column))
	}
	return strings.Join(parts, ", "), nil
}

func (r TextRedaction) expressions(quote func(string) string) (string, string, error) {
	list, err := r.inList(quote)
	if err != nil {
		return "", "", err
	}
	column := quote(r.Column)
	setKey := quote(r.Column + defaultSetKeySuffix)
	text := "CASE WHEN " + list + " THEN NULL ELSE " + column + " END AS " + column
	set := "CASE WHEN " + list + " THEN (" + column + " IS NOT NULL AND btrim(" + column + ") <> '') ELSE NULL END AS " + setKey
	return text, set, nil
}

func (r TextRedaction) inList(quote func(string) string) (string, error) {
	var match strings.Builder
	match.WriteString("(")
	for i, column := range r.MatchColumns {
		if i > 0 {
			match.WriteString(", ")
		}
		match.WriteString(quote(column))
	}
	match.WriteString(") IN (")
	for i, pair := range r.Pairs {
		if i > 0 {
			match.WriteString(", ")
		}
		match.WriteString("(")
		for j, part := range pair {
			if !safeToken(part) {
				return "", fmt.Errorf("activitylog: text redaction token %q is not safe", part)
			}
			if j > 0 {
				match.WriteString(", ")
			}
			match.WriteString("'")
			match.WriteString(part)
			match.WriteString("'")
		}
		match.WriteString(")")
	}
	match.WriteString(")")
	return match.String(), nil
}

func textIsSet(row map[string]any, column, setKey string) bool {
	if raw, ok := row[column]; ok && raw != nil {
		switch value := raw.(type) {
		case string:
			return strings.TrimSpace(value) != ""
		case []byte:
			return strings.TrimSpace(string(value)) != ""
		case bool:
			return value
		}
	}
	return boolish(row[setKey])
}

func boolish(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "t", "true":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func cellString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func safeToken(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
