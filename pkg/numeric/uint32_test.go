package numeric

import (
	"encoding/json"
	"testing"
)

func TestParseUint32(t *testing.T) {
	accepted := map[any]uint32{
		float64(0): 0, float64(4294967295): 4294967295, json.Number("42"): 42, "4294967295": 4294967295,
		int(7): 7, int64(8): 8, uint32(9): 9, uint64(10): 10,
	}
	for raw, want := range accepted {
		if got, ok := ParseUint32(raw); !ok || got != want {
			t.Errorf("%v (%T) = %d, %v", raw, raw, got, ok)
		}
	}
	for _, raw := range []any{float64(4294967296), float64(-1), 0.5, json.Number("1.0"), "", " 1", "+1", "-0", "4294967296", "0x10", int(-1), int64(1 << 32), uint64(1 << 32), nil, true} {
		if got, ok := ParseUint32(raw); ok {
			t.Errorf("%v (%T) accepted as %d", raw, raw, got)
		}
	}
}
