package contract

import "testing"

func TestRejectLegacyErrorShape_RejectsAStringError(t *testing.T) {
	err := RejectLegacyErrorShape(Exchange{
		Step:   "legacy",
		Status: 400,
		Body:   `{"error":"nope"}`,
	})
	if err == nil {
		t.Fatal("a string error must fail even if a snapshot would record it")
	}
}

func TestRejectLegacyErrorShape_AcceptsTheEnvelope(t *testing.T) {
	err := RejectLegacyErrorShape(Exchange{
		Step:   "envelope",
		Status: 404,
		Body:   `{"error":{"code":"not_found","message":"wallet not found"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectLegacyErrorShape_IgnoresSuccess(t *testing.T) {
	err := RejectLegacyErrorShape(Exchange{
		Step:   "ok",
		Status: 200,
		Body:   `{"error":"not an error status"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectLegacyErrorShape_RejectsAnEmptyFailure(t *testing.T) {
	err := RejectLegacyErrorShape(Exchange{Step: "empty", Status: 401, Body: ""})
	if err == nil {
		t.Fatal("an empty non-2xx body must fail")
	}
}

func TestRejectLegacyErrorShape_RequiresFieldErrorsOnValidation(t *testing.T) {
	missing := RejectLegacyErrorShape(Exchange{
		Step:   "validation",
		Status: 422,
		Body:   `{"error":{"code":"validation_failed","message":"validation failed"}}`,
	})
	if missing == nil {
		t.Fatal("validation_failed without errors must fail")
	}
	present := RejectLegacyErrorShape(Exchange{
		Step:   "validation",
		Status: 422,
		Body:   `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"email":["taken"]}}`,
	})
	if present != nil {
		t.Fatal(present)
	}
}

func TestRejectLegacyErrorShape_AllowsADomain422WithoutFieldErrors(t *testing.T) {
	err := RejectLegacyErrorShape(Exchange{
		Step:   "gas",
		Status: 422,
		Body:   `{"error":{"code":"wallet_not_gas_ready","message":"wallet_not_gas_ready"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
}
