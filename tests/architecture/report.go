package architecture

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ModeVariable selects how a check treats the violations it finds:
//
//	report  (default) log the count and what changed against the baseline; never fail
//	ratchet fail only on a violation the baseline does not list
//	enforce fail on any violation
//
// A check becomes blocking by moving it to enforce when its baseline is empty.
const ModeVariable = "ARCH_MODE"

// VerboseVariable, set to 1, lists every violation instead of only the new ones.
const VerboseVariable = "ARCH_VERBOSE"

const (
	modeReport  = "report"
	modeRatchet = "ratchet"
	modeEnforce = "enforce"
)

const baselineDirectory = "testdata/baseline"

var updateBaseline = flag.Bool("update-baseline", false,
	"rewrite tests/architecture/testdata/baseline from the current violations")

// Violations collects the findings of one check. Each entry is a stable line
// (file or package, then what is wrong) without line numbers, so the baseline
// does not churn when unrelated code moves.
type Violations struct {
	counts map[string]int
}

// Add records one occurrence of a finding.
func (v *Violations) Add(format string, args ...any) {
	if v.counts == nil {
		v.counts = map[string]int{}
	}
	v.counts[fmt.Sprintf(format, args...)]++
}

// Lines returns the findings sorted, with a "(xN)" suffix for repeats.
func (v *Violations) Lines() []string {
	return formatEntries(v.counts)
}

// Total returns the number of occurrences, repeats included.
func (v *Violations) Total() int {
	total := 0
	for _, count := range v.counts {
		total += count
	}
	return total
}

// Report applies the current mode to the findings of the check named after
// the running test.
func Report(t *testing.T, violations *Violations) {
	t.Helper()
	name := t.Name()
	current := violations.Lines()
	if *updateBaseline {
		if err := writeBaseline(name, current); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("baseline rewritten: %d entries, %d occurrences", len(current), violations.Total())
		return
	}
	baseline, err := readBaseline(name)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	added, fixed := grownEntries(violations.counts, baseline), grownEntries(baseline, violations.counts)
	t.Logf("%d entries (%d occurrences); baseline %d entries; grown %d; shrunk or gone %d",
		len(current), violations.Total(), len(baseline), len(added), len(fixed))
	listed := added
	if os.Getenv(VerboseVariable) == "1" {
		listed = current
	}
	for _, line := range listed {
		t.Logf("  %s", line)
	}
	if len(fixed) > 0 {
		t.Logf("  %d baseline entries shrank or are gone: run go test ./tests/architecture -update-baseline", len(fixed))
	}
	switch mode() {
	case modeEnforce:
		for _, line := range current {
			t.Errorf("violation: %s", line)
		}
	case modeRatchet:
		for _, line := range added {
			t.Errorf("new violation: %s", line)
		}
	}
}

func mode() string {
	switch value := strings.TrimSpace(os.Getenv(ModeVariable)); value {
	case modeRatchet, modeEnforce:
		return value
	default:
		return modeReport
	}
}

func baselinePath(testName string) string {
	return filepath.Join(baselineDirectory, strings.ReplaceAll(testName, "/", "__")+".txt")
}

func readBaseline(testName string) (map[string]int, error) {
	content, err := os.ReadFile(baselinePath(testName))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := map[string]int{}
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, count := parseEntry(trimmed)
		entries[key] += count
	}
	return entries, nil
}

func writeBaseline(testName string, lines []string) error {
	path := baselinePath(testName)
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	header := "# " + testName + ": known violations, rewritten by go test ./tests/architecture -update-baseline\n"
	return os.WriteFile(path, []byte(header+strings.Join(lines, "\n")+"\n"), 0o600)
}

// formatEntries renders counted findings as sorted lines, with a "(xN)"
// suffix for repeats.
func formatEntries(counts map[string]int) []string {
	lines := make([]string, 0, len(counts))
	for entry, count := range counts {
		lines = append(lines, formatEntry(entry, count))
	}
	sort.Strings(lines)
	return lines
}

func formatEntry(entry string, count int) string {
	if count > 1 {
		return fmt.Sprintf("%s%s%d)", entry, repeatMarker, count)
	}
	return entry
}

const repeatMarker = " (x"

// parseEntry splits a baseline line into its finding and its count.
func parseEntry(line string) (string, int) {
	cut := strings.LastIndex(line, repeatMarker)
	if cut < 0 || !strings.HasSuffix(line, ")") {
		return line, 1
	}
	count, err := strconv.Atoi(line[cut+len(repeatMarker) : len(line)-1])
	if err != nil || count < 1 {
		return line, 1
	}
	return line[:cut], count
}

// grownEntries returns the findings whose count in left exceeds the one in
// right, rendered with the count of left.
func grownEntries(left, right map[string]int) []string {
	grown := map[string]int{}
	for entry, count := range left {
		if count > right[entry] {
			grown[entry] = count
		}
	}
	return formatEntries(grown)
}
