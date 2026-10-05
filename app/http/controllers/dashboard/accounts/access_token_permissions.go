package accounts

import (
	"encoding/json"
	"fmt"
)

// storedAPITokenPermissions keeps an empty grant as empty text. The
// repository omits that value so the jsonb column stays NULL. A non-empty
// grant is the JSON array the plan stores.
func storedAPITokenPermissions(permissions []string) (string, error) {
	if len(permissions) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(permissions)
	if err != nil {
		return "", fmt.Errorf("encode api token permissions: %w", err)
	}
	return string(raw), nil
}
