package rules

import (
	"context"
	"testing"

	contractsvalidation "github.com/goravel/framework/contracts/validation"
	"github.com/goravel/framework/validation"
)

// taggedForm has the destination_tag field of the withdrawal store request.
type taggedForm struct {
	DestinationTag *uint32 `form:"destination_tag" json:"destination_tag,omitempty"`
}

// bindTag validates with the uint32 rule and binds the way ValidateRequest does:
// JSON numbers arrive as float64, form and query values as strings.
func bindTag(t *testing.T, data map[string]any) (*taggedForm, bool) {
	t.Helper()
	validator, err := validation.NewValidation().Make(context.Background(), data, map[string]string{"destination_tag": "uint32"},
		validation.CustomRules([]contractsvalidation.Rule{&Uint32{}}))
	if err != nil {
		t.Fatal(err)
	}
	if validator.Fails() {
		return nil, false
	}
	var form taggedForm
	if err := validator.Bind(&form); err != nil {
		t.Fatalf("bind %v: %v", data, err)
	}
	return &form, true
}

func TestUint32_BindsTheWholeRange(t *testing.T) {
	for raw, want := range map[any]uint32{float64(0): 0, float64(4294967295): 4294967295, "12345": 12345} {
		form, ok := bindTag(t, map[string]any{"destination_tag": raw})
		if !ok || form.DestinationTag == nil || *form.DestinationTag != want {
			t.Fatalf("%v: ok %v form %v", raw, ok, form)
		}
	}
	form, ok := bindTag(t, map[string]any{"amount": "1"})
	if !ok || form.DestinationTag != nil {
		t.Fatalf("a missing tag bound to %v", form.DestinationTag)
	}
}

func TestUint32_RefusesOutOfRangeAndNonIntegers(t *testing.T) {
	for _, raw := range []any{float64(4294967296), float64(-1), 1.5, "abc", "-1", "1e3", true} {
		if _, ok := bindTag(t, map[string]any{"destination_tag": raw}); ok {
			t.Fatalf("%v (%T) passed", raw, raw)
		}
	}
}
