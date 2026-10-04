package settings

import (
	"reflect"
	"testing"
)

func TestCastIn_Int(t *testing.T) {
	t.Parallel()

	got, err := castIn(float64(587), Definition{Type: TypeInt})
	if err != nil || got != "587" {
		t.Fatalf("castIn(587) = %q, %v", got, err)
	}
	if _, err := castIn("not-a-number", Definition{Type: TypeInt}); err == nil {
		t.Fatal("a non-numeric string was stored as an integer")
	}
}

func TestCastIn_Bool(t *testing.T) {
	t.Parallel()

	got, err := castIn(true, Definition{Type: TypeBool})
	if err != nil || got != "true" {
		t.Fatalf("castIn(true) = %q, %v", got, err)
	}
	if _, err := castIn("maybe", Definition{Type: TypeBool}); err == nil {
		t.Fatal("a non-boolean string was stored as a boolean")
	}
}

func TestCastOut_Typed(t *testing.T) {
	t.Parallel()

	if castOut("587", Definition{Type: TypeInt}) != 587 {
		t.Fatal("an integer setting was not returned as an int")
	}
	if castOut("true", Definition{Type: TypeBool}) != true {
		t.Fatal("a boolean setting was not returned as a bool")
	}
	if castOut("smtp", Definition{Type: TypeString}) != "smtp" {
		t.Fatal("a string setting was not returned as a string")
	}
}

func TestCastIn_Decimal(t *testing.T) {
	t.Parallel()

	got, err := castIn("", Definition{Type: TypeDecimal})
	if err != nil || got != "" {
		t.Fatalf("blank decimal = %q, %v", got, err)
	}
	if _, err := castIn("abc", Definition{Type: TypeDecimal}); err == nil {
		t.Fatal("a non-numeric decimal was accepted")
	}
}

func TestCastIn_StringList(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupPriceLookup, keyProviderOrder)
	if !ok || definition.Type != TypeStringList || len(definition.Options) == 0 {
		t.Fatal("provider order is not a string list")
	}

	got, err := castIn([]any{priceProviderCoinGecko, priceProviderCoinAPI}, definition)
	if err != nil || got != priceProviderCoinGecko+","+priceProviderCoinAPI {
		t.Fatalf("list = %q, %v", got, err)
	}
	got, err = castIn([]any{}, definition)
	if err != nil || got != "" {
		t.Fatalf("empty list = %q, %v", got, err)
	}
	if _, err := castIn([]any{"kraken"}, definition); err == nil {
		t.Fatal("a name outside the vocabulary was accepted")
	}
	got, err = castIn(priceProviderCoinGecko+", "+priceProviderCoinAPI, definition)
	if err != nil || got != priceProviderCoinGecko+","+priceProviderCoinAPI {
		t.Fatalf("csv = %q, %v", got, err)
	}
}

func TestCastOut_StringList(t *testing.T) {
	t.Parallel()

	definition := Definition{Type: TypeStringList}
	got := castOut(priceProviderCoinGecko+","+priceProviderCoinAPI, definition)
	want := []string{priceProviderCoinGecko, priceProviderCoinAPI}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("castOut list = %#v", got)
	}
	empty := castOut("", definition)
	if !reflect.DeepEqual(empty, []string{}) {
		t.Fatalf("castOut empty = %#v", empty)
	}
}
