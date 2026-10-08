package pyjson

import (
	"strings"
	"testing"
)

// Vectors produced with CPython 3 json.loads/json.dumps.
const (
	sourceDocument = `{"entries": [{"tag": "a", "n": 3, "f": 1.5, "nested": {"x": [], "y": {}}, "u": "caf\u00e9 \ud83d\ude00", "nul": null, "t": true}], "recordedAt": "2026-10-03T04:30:00+00:00", "empty": []}`
	pythonIndent2  = "{\n  \"entries\": [\n    {\n      \"tag\": \"a\",\n      \"n\": 3,\n      \"f\": 1.5,\n      \"nested\": {\n        \"x\": [],\n        \"y\": {}\n      },\n      \"u\": \"caf\\u00e9 \\ud83d\\ude00\",\n      \"nul\": null,\n      \"t\": true\n    }\n  ],\n  \"recordedAt\": \"2026-10-03T04:30:00+00:00\",\n  \"empty\": []\n}"
	pythonIndent1  = "{\n \"entries\": [\n  {\n   \"tag\": \"a\",\n   \"n\": 3,\n   \"f\": 1.5,\n   \"nested\": {\n    \"x"
)

func TestDecode_Then_DumpsRoundTripsLikePython(t *testing.T) {
	decoded, err := Decode([]byte(sourceDocument))
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Dumps(decoded, Default)
	if err != nil || compact != sourceDocument {
		t.Fatalf("Dumps = %q, %v", compact, err)
	}
	indented, err := DumpsIndent(decoded, 2)
	if err != nil || indented != pythonIndent2 {
		t.Fatalf("DumpsIndent(2) =\n%s\nwant\n%s (%v)", indented, pythonIndent2, err)
	}
	one, err := DumpsIndent(decoded, 1)
	if err != nil || !strings.HasPrefix(one, pythonIndent1) {
		t.Fatalf("DumpsIndent(1) = %q", one)
	}
}

func TestQuote_Escapes_LikeEnsureASCII(t *testing.T) {
	cases := map[string]string{
		"plain":              `"plain"`,
		"a\"b\\c":            `"a\"b\\c"`,
		"\b\f\n\r\t":         `"\b\f\n\r\t"`,
		"\x01\x7f":           `"\u0001\u007f"`,
		"é":                  `"\u00e9"`,
		"\U0001F600":         `"\ud83d\ude00"`,
		"<&> /":              `"<&> /"`,
		"\u2028":             `"\u2028"`,
		"mixed é\U0001F600!": `"mixed \u00e9\ud83d\ude00!"`,
	}
	for input, want := range cases {
		if got := Quote(input); got != want {
			t.Errorf("Quote(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestObject_Set_KeepsPositionOrAppends(t *testing.T) {
	object := Object{{Key: "a", Value: 1}, {Key: "b", Value: 2}}
	updated := object.Set("a", 9).Set("c", 3)
	got, _ := Dumps(updated, Compact)
	if got != `{"a":9,"b":2,"c":3}` {
		t.Fatalf("Set = %s", got)
	}
	if original, _ := Dumps(object, Compact); original != `{"a":1,"b":2}` {
		t.Fatalf("Set mutated its receiver: %s", original)
	}
	if updated.String("missing") != "" {
		t.Fatal("String of a missing key")
	}
}

func TestDecode_Rejects_TrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{"a": 1} {"b": 2}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if _, err := Decode([]byte(`{"a": `)); err == nil {
		t.Fatal("truncated JSON accepted")
	}
}

func TestDumps_Rejects_UnsupportedTypes(t *testing.T) {
	if _, err := Dumps(map[string]int{"a": 1}, Default); err == nil {
		t.Fatal("unordered map accepted")
	}
	if _, err := DumpsIndent(Object{}, -1); err == nil {
		t.Fatal("negative indent accepted")
	}
}
