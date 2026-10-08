package requests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
)

const maxAccountSettingsBodyBytes = 1 << 20

var (
	// ErrAccountSettingsBodyInvalid is a body that is not a JSON object.
	ErrAccountSettingsBodyInvalid = errors.New("invalid request body")
	// ErrAccountSettingsBodyTooLarge is a body over maxAccountSettingsBodyBytes.
	ErrAccountSettingsBodyTooLarge = errors.New("request body is too large")
)

// AccountSettingsDocument reads a partial settings group. An empty body is an
// empty document: the client omits a secret it does not have. The service
// decides which keys belong to the group.
func AccountSettingsDocument(ctx http.Context) (map[string]any, error) {
	if ctx == nil || ctx.Request() == nil {
		return nil, ErrAccountSettingsBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return map[string]any{}, nil
	}

	raw, err := io.ReadAll(io.LimitReader(request.Body, maxAccountSettingsBodyBytes+1))
	if err != nil {
		return nil, ErrAccountSettingsBodyInvalid
	}
	if len(raw) > maxAccountSettingsBodyBytes {
		return nil, ErrAccountSettingsBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || decoder.More() {
		return nil, ErrAccountSettingsBodyInvalid
	}
	if document == nil {
		return nil, ErrAccountSettingsBodyInvalid
	}
	return document, nil
}
