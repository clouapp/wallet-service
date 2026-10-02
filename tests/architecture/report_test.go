package architecture

import (
	"reflect"
	"testing"
)

func TestViolations_LinesCollapseRepeatsIntoACount(t *testing.T) {
	var violations Violations
	violations.Add("%s calls container.Get()", "a.go")
	violations.Add("%s calls container.Get()", "a.go")
	violations.Add("%s calls container.Get()", "b.go")

	want := []string{"a.go calls container.Get() (x2)", "b.go calls container.Get()"}
	if got := violations.Lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines() = %v, want %v", got, want)
	}
	if got := violations.Total(); got != 3 {
		t.Fatalf("Total() = %d, want 3", got)
	}
}

func TestViolations_EmptyHasNoLines(t *testing.T) {
	var violations Violations
	if got := violations.Lines(); len(got) != 0 {
		t.Fatalf("Lines() = %v, want none", got)
	}
	if got := violations.Total(); got != 0 {
		t.Fatalf("Total() = %d, want 0", got)
	}
}

func TestParseEntry_RoundTripsFormatEntry(t *testing.T) {
	cases := map[string]int{
		"a.go calls container.Get()":  1,
		"a.go calls container.Get()(": 7,
		"pkg → other (x → y)":         3,
	}
	for entry, count := range cases {
		gotEntry, gotCount := parseEntry(formatEntry(entry, count))
		if gotEntry != entry || gotCount != count {
			t.Errorf("parseEntry(formatEntry(%q, %d)) = (%q, %d)", entry, count, gotEntry, gotCount)
		}
	}
}

func TestParseEntry_TreatsAMalformedCountAsPartOfTheEntry(t *testing.T) {
	for _, line := range []string{"a.go (xabc)", "a.go (x0)", "a.go (x-2)", "a.go (x3"} {
		entry, count := parseEntry(line)
		if entry != line || count != 1 {
			t.Errorf("parseEntry(%q) = (%q, %d), want the whole line once", line, entry, count)
		}
	}
}

func TestGrownEntries_ReportsOnlyWhatGrewOrAppeared(t *testing.T) {
	baseline := map[string]int{"kept": 2, "shrunk": 5, "gone": 1}
	current := map[string]int{"kept": 2, "shrunk": 4, "grown": 1, "new": 1}

	if got, want := grownEntries(current, baseline), []string{"grown", "new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("grown = %v, want %v", got, want)
	}
	if got, want := grownEntries(baseline, current), []string{"gone", "shrunk (x5)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("shrunk or gone = %v, want %v", got, want)
	}
}

func TestMode_DefaultsToReport(t *testing.T) {
	for value, want := range map[string]string{"": modeReport, "bogus": modeReport, "ratchet": modeRatchet, " enforce ": modeEnforce} {
		t.Setenv(ModeVariable, value)
		if got := mode(); got != want {
			t.Errorf("mode() with %s=%q = %q, want %q", ModeVariable, value, got, want)
		}
	}
}
