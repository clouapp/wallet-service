package requests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
)

const maxChainRPCBodyBytes = 4096

var (
	// ErrChainRPCBodyInvalid is a body that is not a JSON object.
	ErrChainRPCBodyInvalid = errors.New("invalid request body")
	// ErrChainRPCBodyTooLarge is a body over maxChainRPCBodyBytes.
	ErrChainRPCBodyTooLarge = errors.New("request body is too large")
)

// ChainRPCDocument reads one platform chain RPC patch. The service decides
// which keys may be stored. Invalid JSON is refused here.
func ChainRPCDocument(ctx http.Context) (map[string]json.RawMessage, error) {
	if ctx == nil || ctx.Request() == nil {
		return nil, ErrChainRPCBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, ErrChainRPCBodyInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxChainRPCBodyBytes+1))
	if err != nil {
		return nil, ErrChainRPCBodyInvalid
	}
	return ParseChainRPCDocument(raw)
}

// ParseChainRPCDocument decodes one JSON object. A second value, a
// non-object, and a null document are refused.
func ParseChainRPCDocument(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > maxChainRPCBodyBytes {
		return nil, ErrChainRPCBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrChainRPCBodyInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return nil, ErrChainRPCBodyInvalid
	}
	if body == nil {
		return nil, ErrChainRPCBodyInvalid
	}
	return body, nil
}
