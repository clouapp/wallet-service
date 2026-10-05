package resources

import (
	"encoding/json"
	"testing"
)

func TestWithdrawalFailureCodesAreOnTheList(t *testing.T) {
	codes := map[string]string{
		CodeInsufficientFunds:             "insufficient_funds",
		CodeWalletNotGasReady:             "wallet_not_gas_ready",
		CodeUnsupportedChain:              "unsupported_chain",
		CodeSweepLimitExceeded:            "sweep_limit_exceeded",
		CodeInvalidPassphrase:             "invalid_passphrase",
		CodePassphraseTooShort:            "passphrase_too_short",
		CodeConcurrentWithdrawal:          "concurrent_withdrawal",
		CodeTooManyAttempts:               "too_many_attempts",
		CodeSpendingLimitExceeded:         "spending_limit_exceeded",
		CodeSpendingLimitInvalid:          "spending_limit_invalid",
		CodeSpendingLimitQuoteUnavailable: "spending_limit_quote_unavailable",
		CodeInternalError:                 "internal_error",
	}
	if codes[CodeInternal] == "internal_error" || CodeInternal == CodeInternalError {
		t.Fatal("internal_error collapsed into the HTTP 500 code")
	}
	for got, want := range codes {
		if got != want {
			t.Fatalf("code %q, want %q", got, want)
		}
		encoded, err := json.Marshal(NewError(ErrorDeps{Code: got, Message: want}))
		if err != nil {
			t.Fatal(err)
		}
		const prefix = `{"error":{"code":"`
		if len(encoded) < len(prefix) || string(encoded[:len(prefix)]) != prefix {
			t.Fatalf("envelope changed: %s", encoded)
		}
	}
}

func TestNewError_IsTheEnvelope(t *testing.T) {
	encoded, err := json.Marshal(NewError(ErrorDeps{Code: CodeNotFound, Message: "wallet not found"}))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"code":"not_found","message":"wallet not found"}}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

func TestNewError_ExtraFieldsStayInsideError(t *testing.T) {
	encoded, err := json.Marshal(NewError(ErrorDeps{Code: "wallet_not_gas_ready", Message: "wallet_not_gas_ready"}).With(map[string]any{
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
