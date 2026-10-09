package chains

import (
	"encoding/json"

	"github.com/goravel/framework/contracts/http"
)

// UpdateRPCRequest is the body of PATCH /platform/chains/{chainId}/rpc: the
// new rpc_url. The URL is write-only.
type UpdateRPCRequest struct {
	Fields map[string]json.RawMessage
}

// Decode reads one RPC patch. Invalid JSON is refused here; the service
// refuses the keys and values it does not accept.
func (r *UpdateRPCRequest) Decode(ctx http.Context) error {
	fields, err := readObject(ctx)
	if err != nil {
		return err
	}
	r.Fields = fields
	return nil
}
