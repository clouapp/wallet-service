package activitylog

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

type fakePool struct {
	query string
	args  []any
	calls int
}

func (f *fakePool) ExecContext(_ context.Context, q string, a ...any) (sql.Result, error) {
	f.query, f.args, f.calls = q, a, f.calls+1
	return nil, nil
}

// writeEntry has to go through the pool it is HANDED, never one it resolves.
// Inside a transaction that pool is the *sql.Tx, which is what makes the entry
// atomic with the act; resolving a fresh connection would deadlock on the row
// the outer transaction just locked. See the spec §5.3 and §5.6.
func TestWrite_Entry_UsesThePoolItWasGiven(t *testing.T) {
	p := &fakePool{}

	err := writeEntry(context.Background(), p, Entry{
		LogName: "admin", Event: "updated", SubjectType: "publisher", SubjectID: "acme",
		CauserLabel: "ana@acme.example",
		Properties:  map[string]any{"old": map[string]any{"name": "Acme"}},
	})
	if err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("esperava exatamente uma escrita, veio %d", p.calls)
	}
	if !strings.Contains(strings.ToUpper(p.query), "INSERT INTO ACTIVITY_LOG") {
		t.Fatalf("esperava INSERT em activity_log, veio %q", p.query)
	}
	if len(p.args) != 12 {
		t.Fatalf("esperava 12 argumentos parametrizados, veio %d", len(p.args))
	}
}

// An empty LogName would make the row invisible to a screen filtering by
// channel, so it defaults rather than storing "".
func TestWrite_Entry_DefaultsTheLogName(t *testing.T) {
	p := &fakePool{}
	if err := writeEntry(context.Background(), p, Entry{Event: "created"}); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	if p.args[0] != "default" {
		t.Fatalf("log_name: esperava %q, veio %v", "default", p.args[0])
	}
}

// capProperties exists because the column allowlist bounds how MANY columns are
// recorded and not how WIDE they are. An unbounded jsonb, times 365 days of
// retention, times four indexes, is a table size decided by omission.
func TestCap_Properties_TruncatesAndSaysSo(t *testing.T) {
	huge := strings.Repeat("x", maxValueBytes+1)

	got := capProperties(map[string]any{"payload": huge})

	if got["truncated"] != true {
		t.Fatal("esperava truncated=true")
	}
	if v, _ := got["payload"].(string); len(v) > maxValueBytes {
		t.Fatalf("the value was not truncated: %d bytes", len(v))
	}
}

func TestCap_Properties_LeavesSmallDocumentsAlone(t *testing.T) {
	got := capProperties(map[string]any{"name": "Acme"})

	if _, marked := got["truncated"]; marked {
		t.Fatal("a small document should not be marked truncated")
	}
	if got["name"] != "Acme" {
		t.Fatalf("valor alterado: %#v", got["name"])
	}
}

// A document can blow the budget while every individual value is inside its own
// cap — many columns, each legal. Metadata survives, the images do not: which
// record and who touched it is what the trail is FOR; the diff is the detail.
func TestCap_Properties_DropsTheImagesWhenTheDocumentIsOversize(t *testing.T) {
	wide := map[string]any{}
	for i := 0; i < 64; i++ {
		wide[string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("y", maxValueBytes-1)
	}

	got := capProperties(wide)

	if got["oversize"] != true || got["truncated"] != true {
		t.Fatalf("esperava oversize e truncated, veio %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > maxDocumentBytes {
		t.Fatalf("documento continua acima do teto: %d bytes", len(encoded))
	}
}

func TestCap_Properties_HandlesNil(t *testing.T) {
	if got := capProperties(nil); len(got) != 0 {
		t.Fatalf("esperava documento vazio, veio %#v", got)
	}
}
