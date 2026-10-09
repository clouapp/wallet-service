package chains

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
)

const maxBodyBytes = 4096

var (
	// ErrBodyInvalid is a body that is not a JSON object.
	ErrBodyInvalid = errors.New("invalid request body")
	// ErrBodyTooLarge is a body over maxBodyBytes.
	ErrBodyTooLarge = errors.New("request body is too large")
)

// readObject reads the body of a chain patch. The service decides which keys
// may be stored.
func readObject(ctx http.Context) (map[string]json.RawMessage, error) {
	if ctx == nil || ctx.Request() == nil {
		return nil, ErrBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, ErrBodyInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBodyBytes+1))
	if err != nil {
		return nil, ErrBodyInvalid
	}
	return parseObject(raw)
}

// parseObject decodes one JSON object. A second value, a non-object, and a
// null document are refused; a well-formed object, including an empty one,
// is returned as raw fields.
func parseObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > maxBodyBytes {
		return nil, ErrBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrBodyInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return nil, ErrBodyInvalid
	}
	if body == nil {
		return nil, ErrBodyInvalid
	}
	return body, nil
}
