package features

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/goravel/framework/contracts/http"

	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// ScopeFlag is one flag in a bulk scoped write. The service decides whether
// the key exists.
type ScopeFlag struct {
	Key     string `form:"key"     json:"key"     example:"withdrawals-enabled"`
	Enabled bool   `form:"enabled" json:"enabled" example:"true"`
}

// UpdateScopeRequest is the body of PUT /platform/features/{scope}/{id}:
// {"features":[{"key","enabled"}]}.
type UpdateScopeRequest struct {
	Features []ScopeFlag `form:"features" json:"features"`
}

// Writes maps the decoded flags to the service's input.
func (r *UpdateScopeRequest) Writes() []featuressvc.ScopedWrite {
	writes := make([]featuressvc.ScopedWrite, 0, len(r.Features))
	for _, flag := range r.Features {
		writes = append(writes, featuressvc.ScopedWrite{Key: flag.Key, Enabled: flag.Enabled})
	}
	return writes
}

// Decode reads the bulk body. Every entry is returned before the caller
// stores any of them.
func (r *UpdateScopeRequest) Decode(ctx http.Context) error {
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

// parse decodes a bulk flag write. Keys are trimmed.
func (r *UpdateScopeRequest) parse(raw []byte) error {
	if len(raw) > maxBodyBytes {
		return ErrBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ErrBodyInvalid
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var body struct {
		Features []struct {
			Key     string `json:"key"`
			Enabled *bool  `json:"enabled"`
		} `json:"features"`
	}
	if err := decoder.Decode(&body); err != nil || decoder.More() {
		return ErrBodyInvalid
	}
	if len(body.Features) == 0 {
		return ErrFeaturesRequired
	}

	seen := make(map[string]struct{}, len(body.Features))
	flags := make([]ScopeFlag, 0, len(body.Features))
	for _, item := range body.Features {
		key := strings.TrimSpace(item.Key)
		if key == "" || item.Enabled == nil {
			return ErrBodyInvalid
		}
		if _, dup := seen[key]; dup {
			return ErrDuplicateKey
		}
		seen[key] = struct{}{}
		flags = append(flags, ScopeFlag{Key: key, Enabled: *item.Enabled})
	}
	r.Features = flags
	return nil
}
