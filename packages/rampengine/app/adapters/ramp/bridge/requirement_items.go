package bridge

import (
	"encoding/json"
	"fmt"
)

// requirementItems is Bridge's missing.all_of / missing.any_of list. The
// sandbox sends string codes ("terms_of_service_v1"), newer shapes wrap each
// in an object, and groups nest ({"any_of":["live_selfie","selfie_document"]}).
// An item that names no requirement is skipped: failing it would fail the
// whole customer decode.
type requirementItems []string

func (r *requirementItems) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = nil
		return nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("bridge: requirement list: %w", err)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		var code string
		if json.Unmarshal(item, &code) == nil {
			if code != "" {
				out = append(out, code)
			}
			continue
		}

		var obj map[string]json.RawMessage
		if json.Unmarshal(item, &obj) != nil {
			continue
		}
		if direct := requirementCodeFromObject(obj); direct != "" {
			out = append(out, direct)
		}
		for _, key := range []string{"any_of", "all_of"} {
			var nested requirementItems
			if json.Unmarshal(obj[key], &nested) == nil {
				out = append(out, nested...)
			}
		}
	}
	*r = out
	return nil
}

func requirementCodeFromObject(obj map[string]json.RawMessage) string {
	for _, key := range []string{"type", "name", "requirement", "code"} {
		var value string
		if json.Unmarshal(obj[key], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}
