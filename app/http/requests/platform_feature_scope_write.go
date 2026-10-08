package requests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/goravel/framework/contracts/http"
)

var (
	// ErrPlatformFeatureScopeBodyInvalid is a bulk flag write that is not
	// {"features":[{"key","enabled"}]}.
	ErrPlatformFeatureScopeBodyInvalid = errors.New("invalid request body")
	// ErrPlatformFeatureScopeBodyTooLarge is a body over maxAccountFeatureBodyBytes.
	ErrPlatformFeatureScopeBodyTooLarge = errors.New("request body is too large")
	// ErrPlatformFeatureScopeFeaturesRequired is a body that omits features
	// or sends an empty list.
	ErrPlatformFeatureScopeFeaturesRequired = errors.New("features is required")
	// ErrPlatformFeatureScopeDuplicate is the same key twice in one body.
	ErrPlatformFeatureScopeDuplicate = errors.New("feature key is duplicated")
)

// FeatureScopeWrite is one flag in a bulk scoped write. The service decides
// whether the key exists.
type FeatureScopeWrite struct {
	Key     string
	Enabled bool
}

// PlatformFeatureScopeWrites reads the bulk body for
// PUT /v1/platform/features/{scope}/{id}.
func PlatformFeatureScopeWrites(ctx http.Context) ([]FeatureScopeWrite, error) {
	if ctx == nil || ctx.Request() == nil {
		return nil, ErrPlatformFeatureScopeBodyInvalid
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, ErrPlatformFeatureScopeBodyInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxAccountFeatureBodyBytes+1))
	if err != nil {
		return nil, ErrPlatformFeatureScopeBodyInvalid
	}
	return ParsePlatformFeatureScopeWrites(raw)
}

// ParsePlatformFeatureScopeWrites decodes a bulk flag write. Every entry is
// returned before the caller stores any of them.
func ParsePlatformFeatureScopeWrites(raw []byte) ([]FeatureScopeWrite, error) {
	if len(raw) > maxAccountFeatureBodyBytes {
		return nil, ErrPlatformFeatureScopeBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrPlatformFeatureScopeBodyInvalid
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
		return nil, ErrPlatformFeatureScopeBodyInvalid
	}
	if len(body.Features) == 0 {
		return nil, ErrPlatformFeatureScopeFeaturesRequired
	}

	seen := make(map[string]struct{}, len(body.Features))
	out := make([]FeatureScopeWrite, 0, len(body.Features))
	for _, item := range body.Features {
		key := strings.TrimSpace(item.Key)
		if key == "" || item.Enabled == nil {
			return nil, ErrPlatformFeatureScopeBodyInvalid
		}
		if _, dup := seen[key]; dup {
			return nil, ErrPlatformFeatureScopeDuplicate
		}
		seen[key] = struct{}{}
		out = append(out, FeatureScopeWrite{Key: key, Enabled: *item.Enabled})
	}
	return out, nil
}
