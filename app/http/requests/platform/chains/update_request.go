package chains

import (
	"encoding/json"

	"github.com/goravel/framework/contracts/http"
)

// UpdateRequest is the body of PATCH /platform/chains/{chainId}: the sweep
// thresholds to change. An omitted field is left unchanged.
type UpdateRequest struct {
	Fields map[string]json.RawMessage
}

// Decode reads one threshold patch. Invalid JSON is refused here; the service
// refuses the keys and values it does not accept.
func (r *UpdateRequest) Decode(ctx http.Context) error {
	fields, err := readObject(ctx)
	if err != nil {
		return err
	}
	r.Fields = fields
	return nil
}
