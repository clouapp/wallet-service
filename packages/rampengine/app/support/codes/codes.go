// Package codes indexes a fixed set of implementations by the code each one
// reports — the lookup the ramp, postal and mailer factories share — and parses
// the stored order a router walks those codes in.
package codes

import (
	"maps"
	"slices"
	"strings"
)

// Map holds one item per code.
type Map[T any] map[string]T

// Index registers items under the code each reports. A later item with the
// same code replaces an earlier one, which keeps test setup free to override a
// single entry; a nil item is skipped.
func Index[T any](code func(T) string, items ...T) Map[T] {
	m := make(Map[T], len(items))
	for _, item := range items {
		if any(item) == nil {
			continue
		}
		m[code(item)] = item
	}
	return m
}

// Keys returns every code, sorted, so a sweep over all of them has a
// deterministic order.
func (m Map[T]) Keys() []string {
	return slices.Sorted(maps.Keys(m))
}

// ParseOrder splits a stored comma-separated order. A blank entry is dropped
// rather than kept: "bridge,," must not put an empty code in the chain.
func ParseOrder(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if code := strings.TrimSpace(part); code != "" {
			out = append(out, code)
		}
	}
	return out
}
