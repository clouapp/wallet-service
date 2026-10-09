package features

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/goravel/framework/contracts/http"
)

// UpdateRequest is the body of PATCH /platform/features/{key} and of
// PUT /platform/features/{scope}/{id}/{feature}: {"enabled": <bool>}.
type UpdateRequest struct {
	Enabled bool `form:"enabled" json:"enabled" example:"true"`
}

// Decode reads the one boolean a flag write accepts. A missing enabled, a
// non-boolean, or any other field is refused. The service decides whether
// the path key exists.
func (r *UpdateRequest) Decode(ctx http.Context) error {
	if ctx == nil || ctx.Request() == nil {
		return ErrBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return ErrBodyInvalid
	}

	raw, err := io.ReadAll(io.LimitReader(request.Body, maxBodyBytes+1))
	if err != nil {
		return ErrBodyInvalid
	}
	return r.parse(raw)
}

// parse decodes one flag write. Empty input and any shape other than a
// single enabled boolean are errors.
func (r *UpdateRequest) parse(raw []byte) error {
	if len(raw) > maxBodyBytes {
		return ErrBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ErrBodyInvalid
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return ErrBodyInvalid
	}
	if body.Enabled == nil {
		return ErrEnabledRequired
	}
	r.Enabled = *body.Enabled
	return nil
}
