package chains

import (
	"encoding/json"

	"github.com/goravel/framework/contracts/http"
)

// RPCEndpointsRequest is the body of a change to a chain's endpoint list
// (/platform/chains/{chainId}/rpc-endpoints): {"url","position"} to add,
// {"urls"} to replace, {"order"} to reorder, {"enabled"} to enable or disable.
// The URLs are write-only.
type RPCEndpointsRequest struct {
	Fields map[string]json.RawMessage
}

// Decode reads one change. Invalid JSON is refused here; the service refuses the
// keys and values it does not accept.
func (r *RPCEndpointsRequest) Decode(ctx http.Context) error {
	fields, err := readObject(ctx)
	if err != nil {
		return err
	}
	r.Fields = fields
	return nil
}
