package requests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
)

const maxChainThresholdBodyBytes = 4096

var (
	// ErrChainThresholdBodyInvalid is a body that is not a JSON object.
	ErrChainThresholdBodyInvalid = errors.New("invalid request body")
	// ErrChainThresholdBodyTooLarge is a body over maxChainThresholdBodyBytes.
	ErrChainThresholdBodyTooLarge = errors.New("request body is too large")
)

// ChainThresholdDocument reads one platform chain patch. The service decides
// which keys may be stored. Invalid JSON is refused here; a well-formed
// object, including an empty one, is returned as raw fields.
func ChainThresholdDocument(ctx http.Context) (map[string]json.RawMessage, error) {
	if ctx == nil || ctx.Request() == nil {
		return nil, ErrChainThresholdBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, ErrChainThresholdBodyInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxChainThresholdBodyBytes+1))
	if err != nil {
		return nil, ErrChainThresholdBodyInvalid
	}
	return ParseChainThresholdDocument(raw)
}

// ParseChainThresholdDocument decodes one JSON object. A second value, a
// non-object, and a null document are refused.
func ParseChainThresholdDocument(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > maxChainThresholdBodyBytes {
		return nil, ErrChainThresholdBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrChainThresholdBodyInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return nil, ErrChainThresholdBodyInvalid
	}
	if body == nil {
		return nil, ErrChainThresholdBodyInvalid
	}
	return body, nil
}
