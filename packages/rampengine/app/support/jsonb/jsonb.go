// Package jsonb encodes values for the NOT NULL jsonb columns, which take
// neither NULL nor an empty string.
package jsonb

import "encoding/json"

// Object renders value for a jsonb column. An absent or unencodable value is an
// empty object: the data it carries is auxiliary, so losing it must not fail
// the write that holds it. Nothing is logged, because it may carry customer
// fields.
func Object(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
