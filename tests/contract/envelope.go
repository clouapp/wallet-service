package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RejectLegacyErrorShape fails when a non-2xx JSON body is empty, carries a
// string "error", or lacks a non-empty code and message. A 422 whose code is
// validation_failed must also carry the per-field "errors" map. Domain 422s
// are not required to. This runs on the live exchange before the snapshot is
// written or compared, so -update-contract cannot bless the old shape.
func RejectLegacyErrorShape(exchange Exchange) error {
	if exchange.Status < 400 {
		return nil
	}
	body := strings.TrimSpace(exchange.Body)
	if body == "" {
		return fmt.Errorf("%s: status %d has an empty body", exchange.Step, exchange.Status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return fmt.Errorf("%s: status %d body is not a JSON object: %w", exchange.Step, exchange.Status, err)
	}
	raw, ok := payload["error"]
	if !ok {
		return fmt.Errorf("%s: status %d body has no error object", exchange.Step, exchange.Status)
	}
	if _, isString := raw.(string); isString {
		return fmt.Errorf("%s: status %d still uses a string error", exchange.Step, exchange.Status)
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: status %d error is not an object", exchange.Step, exchange.Status)
	}
	code, _ := object["code"].(string)
	message, _ := object["message"].(string)
	if strings.TrimSpace(code) == "" || strings.TrimSpace(message) == "" {
		return fmt.Errorf("%s: status %d error needs a non-empty code and message", exchange.Step, exchange.Status)
	}
	if exchange.Status == 422 && code == "validation_failed" {
		fields, isMap := payload["errors"].(map[string]any)
		if !isMap || len(fields) == 0 {
			return fmt.Errorf("%s: validation_failed needs a non-empty errors map", exchange.Step)
		}
	}
	return nil
}
