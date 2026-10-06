package responses

import (
	"encoding/json"
	"net/http"
	"testing"

	contractshttp "github.com/goravel/framework/contracts/http"
)

func TestWrap_Legacy_StringErrorBecomesEnvelope(t *testing.T) {
	got, ok := WrapLegacy(http.StatusNotFound, contractshttp.Json{"error": "wallet not found"})
	if !ok {
		t.Fatal("expected a legacy error map to wrap")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"not_found","message":"wallet not found"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestWrap_Legacy_MachineCodeIsTheCode(t *testing.T) {
	got, ok := WrapLegacy(http.StatusUnprocessableEntity, map[string]any{
		"error":  "wallet_not_gas_ready",
		"action": "fund_base_address",
	})
	if !ok {
		t.Fatal("expected wrap")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"action":"fund_base_address","code":"wallet_not_gas_ready","message":"wallet_not_gas_ready"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestWrap_Legacy_ExplicitCodeAndExtras(t *testing.T) {
	got, ok := WrapLegacy(http.StatusUnprocessableEntity, contractshttp.Json{
		"error": "fee estimation unavailable",
		"code":  "FEE_ESTIMATE_FAILED",
	})
	if !ok {
		t.Fatal("expected wrap")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"FEE_ESTIMATE_FAILED","message":"fee estimation unavailable"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestWrap_Legacy_SignatureCode(t *testing.T) {
	got, ok := WrapLegacy(http.StatusUnauthorized, contractshttp.Json{"error": "missing request signature"})
	if !ok {
		t.Fatal("expected wrap")
	}
	if got.Error.Code != CodeInvalidSignature {
		t.Fatalf("code = %s", got.Error.Code)
	}
}

func TestWrap_Legacy_LeavesSuccessBodiesAlone(t *testing.T) {
	if _, ok := WrapLegacy(http.StatusOK, contractshttp.Json{"status": "ok"}); ok {
		t.Fatal("a body without an error string must not wrap")
	}
	if _, ok := WrapLegacy(http.StatusBadRequest, contractshttp.Json{"error": map[string]any{"code": "x"}}); ok {
		t.Fatal("an error object must not wrap again")
	}
}

func TestField_Messages_SortsRules(t *testing.T) {
	messages := FieldMessages(fakeErrors{all: map[string]map[string]string{
		"email": {"email": "Please provide a valid email address", "required": "Email address is required"},
	}})
	got := messages["email"]
	if len(got) != 2 || got[0] != "Please provide a valid email address" || got[1] != "Email address is required" {
		t.Fatalf("got %#v", got)
	}
}

type fakeErrors struct {
	all map[string]map[string]string
}

func (f fakeErrors) One(_ ...string) string            { return "" }
func (f fakeErrors) Get(key string) map[string]string  { return f.all[key] }
func (f fakeErrors) All() map[string]map[string]string { return f.all }
func (f fakeErrors) Has(key string) bool               { _, ok := f.all[key]; return ok }
