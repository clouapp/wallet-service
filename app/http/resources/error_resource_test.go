package resources

import (
	"encoding/json"
	"testing"
)

func TestNewError_IsTheEnvelope(t *testing.T) {
	encoded, err := json.Marshal(NewError(CodeNotFound, "wallet not found"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"not_found","message":"wallet not found"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestNewError_ExtraFieldsStayInsideError(t *testing.T) {
	encoded, err := json.Marshal(NewError("wallet_not_gas_ready", "wallet_not_gas_ready").With(map[string]any{
		"action": "fund_base_address",
	}))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"action":"fund_base_address","code":"wallet_not_gas_ready","message":"wallet_not_gas_ready"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestNewValidation_Is422Shape(t *testing.T) {
	encoded, err := json.Marshal(NewValidation(map[string][]string{
		"email": {"Email address is required"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"email":["Email address is required"]}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestNewValidation_NilFieldsAreAnEmptyObject(t *testing.T) {
	encoded, err := json.Marshal(NewValidation(nil))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestNewPage_KeepsTheListEnvelope(t *testing.T) {
	encoded, err := json.Marshal(NewPage([]string{"a"}, 3, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"data":["a"],"total":3,"limit":20,"offset":0}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}
