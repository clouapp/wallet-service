package requests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"
)

const maxAccountFeatureBodyBytes = 4096

var (
	// ErrAccountFeatureBodyInvalid is a body that is not {"enabled": <bool>}.
	ErrAccountFeatureBodyInvalid = errors.New("invalid request body")
	// ErrAccountFeatureBodyTooLarge is a body over maxAccountFeatureBodyBytes.
	ErrAccountFeatureBodyTooLarge = errors.New("request body is too large")
	// ErrAccountFeatureEnabledRequired is a body that omits enabled.
	ErrAccountFeatureEnabledRequired = errors.New("enabled is required")
)

// AccountFeatureEnabled reads the one boolean a flag write accepts.
// A missing enabled, a non-boolean, or any other field is refused. The
// service decides whether the path key exists.
func AccountFeatureEnabled(ctx http.Context) (bool, error) {
	if ctx == nil || ctx.Request() == nil {
		return false, ErrAccountFeatureBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return false, ErrAccountFeatureBodyInvalid
	}

	raw, err := io.ReadAll(io.LimitReader(request.Body, maxAccountFeatureBodyBytes+1))
	if err != nil {
		return false, ErrAccountFeatureBodyInvalid
	}
	return ParseAccountFeatureEnabled(raw)
}

// ParseAccountFeatureEnabled decodes one flag write. Empty input and any
// shape other than a single enabled boolean are errors.
func ParseAccountFeatureEnabled(raw []byte) (bool, error) {
	if len(raw) > maxAccountFeatureBodyBytes {
		return false, ErrAccountFeatureBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, ErrAccountFeatureBodyInvalid
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return false, ErrAccountFeatureBodyInvalid
	}
	if body.Enabled == nil {
		return false, ErrAccountFeatureEnabledRequired
	}
	return *body.Enabled, nil
}
