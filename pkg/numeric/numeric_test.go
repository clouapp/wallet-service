package numeric

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func mustParse(t *testing.T, text string) decimal.Decimal {
	t.Helper()
	value, err := Parse("test value", text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func TestDecimalMarshalsAsABareNumberWithExactDigits(t *testing.T) {
	raw, err := json.Marshal(map[string]Decimal{"price": NewDecimal(mustParse(t, "65000.1234567890"))})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"price":65000.123456789}` {
		t.Fatalf("json = %s", raw)
	}
}

func TestDecimalUnmarshalsNumbersAndStrings(t *testing.T) {
	var payload struct {
		Number Decimal `json:"number"`
		Text   Decimal `json:"text"`
	}
	if err := json.Unmarshal([]byte(`{"number":0.1,"text":"0.2"}`), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Number.Add(payload.Text.Decimal).Equal(mustParse(t, "0.3")) {
		t.Fatalf("0.1 + 0.2 = %s, want exactly 0.3", payload.Number.Add(payload.Text.Decimal))
	}
}

func TestNullDecimalOmitsNullAndKeepsZero(t *testing.T) {
	type view struct {
		Missing NullDecimal `json:"missing,omitzero"`
		Zero    NullDecimal `json:"zero,omitzero"`
		Value   NullDecimal `json:"value,omitzero"`
		Plain   NullDecimal `json:"plain"`
	}
	raw, err := json.Marshal(view{
		Zero:  NewNullDecimal(decimal.Zero),
		Value: NewNullDecimal(mustParse(t, "1.2500")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"zero":0,"value":1.25,"plain":null}` {
		t.Fatalf("json = %s", raw)
	}
}

func TestNullDecimalWithStaleDigitsButNoValueIsStillNull(t *testing.T) {
	stale := NullDecimal{NullDecimal: decimal.NullDecimal{Decimal: mustParse(t, "9"), Valid: false}}
	if !stale.IsZero() {
		t.Fatal("an invalid NullDecimal must count as NULL")
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatalf("json = %s, want null", raw)
	}
}

func TestNullDecimalUnmarshalsNull(t *testing.T) {
	var payload struct {
		Value NullDecimal `json:"value"`
	}
	if err := json.Unmarshal([]byte(`{"value":null}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Value.Valid {
		t.Fatal("null must decode to SQL NULL")
	}
	if err := json.Unmarshal([]byte(`{"value":4.97}`), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Value.Valid || !payload.Value.Decimal.Equal(mustParse(t, "4.97")) {
		t.Fatalf("value = %+v", payload.Value)
	}
}

func TestNullDecimalFromPointer(t *testing.T) {
	if NullDecimalFromPointer(nil).Valid {
		t.Fatal("nil must map to NULL")
	}
	value := mustParse(t, "2")
	if got := NullDecimalFromPointer(&value); !got.Valid || !got.Decimal.Equal(value) {
		t.Fatalf("got %+v", got)
	}
}

func TestNullDecimalPointer(t *testing.T) {
	if (NullDecimal{}).Pointer() != nil {
		t.Fatal("NULL must map to nil")
	}
	if got := NewNullDecimal(mustParse(t, "3")).Pointer(); got == nil || !got.Equal(mustParse(t, "3")) {
		t.Fatalf("got %v", got)
	}
}

func TestDecimalAndNullDecimalRoundTripTheDatabaseDriverForms(t *testing.T) {
	var price Decimal
	if err := price.Scan("0.1960784314"); err != nil {
		t.Fatal(err)
	}
	stored, err := price.Value()
	if err != nil || stored != "0.1960784314" {
		t.Fatalf("value = %v, %v", stored, err)
	}

	var missing NullDecimal
	if err := missing.Scan(nil); err != nil || missing.Valid {
		t.Fatalf("scan nil = %+v, %v", missing, err)
	}
	if stored, err := missing.Value(); err != nil || stored != nil {
		t.Fatalf("value of NULL = %v, %v", stored, err)
	}
}

func TestParseAcceptsPlainAndExponentNotation(t *testing.T) {
	for text, want := range map[string]string{"1.25": "1.25", " 0.10 ": "0.1", "1e-3": "0.001", "-2": "-2", ".5": "0.5", "+1": "1"} {
		if got := mustParse(t, text); !got.Equal(decimal.RequireFromString(want)) {
			t.Fatalf("parse %q = %s, want %s", text, got, want)
		}
	}
}

func TestParseRejectsNonNumbers(t *testing.T) {
	for _, text := range []string{"", "  ", "NaN", "Inf", "-Inf", "infinity", "0x1p-2", "1,5", "abc", "1.2.3"} {
		if _, err := Parse("amount", text); err == nil {
			t.Fatalf("parse %q: expected an error", text)
		}
	}
}

func TestParseNonNegative(t *testing.T) {
	if _, err := ParseNonNegative("cap", "-0.01"); !errors.Is(err, ErrNegative) {
		t.Fatalf("err = %v, want ErrNegative", err)
	}
	if got, err := ParseNonNegative("cap", "0"); err != nil || !got.IsZero() {
		t.Fatalf("got %v, %v", got, err)
	}
}
