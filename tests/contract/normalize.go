// Package contract records the HTTP contract of the API — method, path,
// status, content type and body of a fixed scenario — so a refactor can prove
// it answers byte for byte what it answered before (alignment plan §6).
package contract

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	uuidPattern      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	timestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	jwtPattern       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	// Fixture wallet labels append " <run nonce>-<sequence>" so a leftover row
	// does not collide. The recorded contract keeps the stable prefix.
	fixtureWalletLabel = regexp.MustCompile(` test wallet [0-9a-f]{8}-\d+`)
)

// Normalizer replaces the values that differ between two runs of the same
// scenario with stable placeholders, leaving every other byte as it was.
// UUIDs keep their identity: the n-th distinct UUID of a run is <uuid:n> in
// every body, so a cross-reference between two responses survives.
type Normalizer struct {
	uuids          map[string]int
	volatileFields []string
	volatileRegexp *regexp.Regexp
}

// NewNormalizer returns a normalizer that also masks the string or number
// value of every JSON field named in volatileFields (secrets and random
// tokens that have no recognizable shape).
func NewNormalizer(volatileFields ...string) *Normalizer {
	fields := append([]string(nil), volatileFields...)
	sort.Strings(fields)
	normalizer := &Normalizer{uuids: map[string]int{}, volatileFields: fields}
	if len(fields) > 0 {
		quoted := make([]string, len(fields))
		for i, field := range fields {
			quoted[i] = regexp.QuoteMeta(field)
		}
		normalizer.volatileRegexp = regexp.MustCompile(
			`"(` + strings.Join(quoted, "|") + `)":\s*("(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?)`)
	}
	return normalizer
}

// Normalize returns text with its volatile values replaced.
func (n *Normalizer) Normalize(text string) string {
	if n.volatileRegexp != nil {
		text = n.volatileRegexp.ReplaceAllStringFunc(text, func(match string) string {
			field := n.volatileRegexp.FindStringSubmatch(match)[1]
			return fmt.Sprintf(`"%s":"<%s>"`, field, field)
		})
	}
	text = fixtureWalletLabel.ReplaceAllString(text, " test wallet")
	text = jwtPattern.ReplaceAllString(text, "<jwt>")
	text = uuidPattern.ReplaceAllStringFunc(text, func(match string) string {
		key := strings.ToLower(match)
		index, seen := n.uuids[key]
		if !seen {
			index = len(n.uuids) + 1
			n.uuids[key] = index
		}
		return fmt.Sprintf("<uuid:%d>", index)
	})
	return timestampPattern.ReplaceAllString(text, "<time>")
}
