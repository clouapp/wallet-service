package rules

import (
	"context"
	"errors"
	"testing"
)

type fakeRows struct {
	count int64
	err   error
	table string
	col   string
	value string
}

func (f *fakeRows) CountEquals(_ context.Context, table, column, value string) (int64, error) {
	f.table = table
	f.col = column
	f.value = value
	return f.count, f.err
}

func TestDB_Exists_AsksThePort(t *testing.T) {
	rows := &fakeRows{count: 1}
	rule := NewDBExists(rows)
	if !rule.Passes(context.Background(), nil, "eth", "chains", "id") {
		t.Fatal("expected an existing chain to pass")
	}
	if rows.table != "chains" || rows.col != "id" || rows.value != "eth" {
		t.Fatalf("port called with %s.%s = %s", rows.table, rows.col, rows.value)
	}

	rows.count = 0
	if rule.Passes(context.Background(), nil, "dogecoin", "chains", "id") {
		t.Fatal("expected a missing chain to fail")
	}
}

func TestDB_Exists_PassesWhenTheReadFailsOrTheValueIsBlank(t *testing.T) {
	rule := NewDBExists(&fakeRows{err: errors.New("down")})
	if !rule.Passes(context.Background(), nil, "eth", "chains", "id") {
		t.Fatal("expected a failed read to pass")
	}
	if !rule.Passes(context.Background(), nil, "", "chains", "id") {
		t.Fatal("expected a blank value to pass")
	}
	if !rule.Passes(context.Background(), nil, "eth") {
		t.Fatal("expected a rule without a column to pass")
	}
}

func TestUnique_Asks_ThePort(t *testing.T) {
	rows := &fakeRows{count: 0}
	rule := NewUnique(rows)
	if !rule.Passes(context.Background(), nil, "new@example.com", "users", "email") {
		t.Fatal("expected an unused email to pass")
	}
	if rows.table != "users" || rows.col != "email" || rows.value != "new@example.com" {
		t.Fatalf("port called with %s.%s = %s", rows.table, rows.col, rows.value)
	}

	rows.count = 1
	if rule.Passes(context.Background(), nil, "taken@example.com", "users", "email") {
		t.Fatal("expected a taken email to fail")
	}
}

func TestUnique_Passes_WhenTheReadFails(t *testing.T) {
	rule := NewUnique(&fakeRows{err: errors.New("down")})
	if !rule.Passes(context.Background(), nil, "taken@example.com", "users", "email") {
		t.Fatal("expected a failed read to pass")
	}
}

func TestNew_DB_ExistsRejectsANilPort(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NewDBExists to panic")
		}
	}()
	NewDBExists(nil)
}

func TestNew_Unique_RejectsANilPort(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NewUnique to panic")
		}
	}()
	NewUnique(nil)
}
