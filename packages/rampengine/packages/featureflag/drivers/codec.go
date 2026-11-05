package drivers

import (
	"encoding/json"
	"fmt"
)

// encodeValue serializes a feature value for storage. Every driver uses it so
// their round-trip behavior is identical.
func encodeValue(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("featureflag: encode value: %w", err)
	}
	return b, nil
}

// decodeValue is the inverse of encodeValue. JSON's type mapping applies:
// numbers decode to float64, objects to map[string]any, etc.
func decodeValue(b []byte) (any, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("featureflag: decode value: %w", err)
	}
	return v, nil
}
