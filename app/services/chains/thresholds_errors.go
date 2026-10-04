package chains

import "errors"

var (
	// ErrNotFound is a chain id that does not exist. The route answers 404
	// before it answers 403.
	ErrNotFound = errors.New("chain not found")
	// ErrPlatformForbidden is a caller who is not a platform admin. S1.4.7
	// names chains.view and chains.update; this branch has no platform
	// permission catalog, so the gate is the platform_admins row the other
	// /v1/platform routes use. The declared pair is not a second gate.
	ErrPlatformForbidden = errors.New("you do not have permission to update chains")
)

// ValidationError is a 422 body: one list of messages per field. The row is
// left unchanged.
type ValidationError struct {
	Fields map[string][]string
}

func (e *ValidationError) Error() string {
	return "validation failed"
}

func (e *ValidationError) add(field, message string) {
	if e == nil || field == "" || message == "" {
		return
	}
	if e.Fields == nil {
		e.Fields = map[string][]string{}
	}
	e.Fields[field] = append(e.Fields[field], message)
}

func (e *ValidationError) empty() bool {
	return e == nil || len(e.Fields) == 0
}
