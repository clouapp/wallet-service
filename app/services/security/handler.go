package security

import (
	contractslog "github.com/goravel/framework/contracts/log"
)

// NewRedactingHandler wraps a Goravel log handler so every message and field
// is passed through RedactText / RedactError before the inner handler writes
// it. The framework's SQL logger interpolates bind arguments into that
// message, so the sink is the last place a credential can still be caught.
func NewRedactingHandler(inner contractslog.Handler) contractslog.Handler {
	if inner == nil {
		return nil
	}
	return redactingHandler{inner: inner}
}

type redactingHandler struct {
	inner contractslog.Handler
}

func (h redactingHandler) Enabled(level contractslog.Level) bool {
	return h.inner.Enabled(level)
}

func (h redactingHandler) Handle(entry contractslog.Entry) error {
	return h.inner.Handle(redactedEntry{Entry: entry})
}

// redactedEntry keeps the inner entry's accessors and overrides the ones that
// carry free-form text.
type redactedEntry struct {
	contractslog.Entry
}

func (e redactedEntry) Message() string { return RedactText(e.Entry.Message()) }
func (e redactedEntry) Code() string    { return RedactText(e.Entry.Code()) }
func (e redactedEntry) Domain() string  { return RedactText(e.Entry.Domain()) }
func (e redactedEntry) Hint() string    { return RedactText(e.Entry.Hint()) }

func (e redactedEntry) Data() contractslog.Data {
	src := e.Entry.Data()
	if len(src) == 0 {
		return src
	}
	out := make(contractslog.Data, len(src))
	for k, v := range src {
		out[k] = redactField(k, v)
	}
	return out
}

func (e redactedEntry) With() map[string]any {
	src := e.Entry.With()
	if len(src) == 0 {
		return src
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = redactField(k, v)
	}
	return out
}

func (e redactedEntry) Request() map[string]any  { return redactStringMap(e.Entry.Request()) }
func (e redactedEntry) Response() map[string]any { return redactStringMap(e.Entry.Response()) }
func (e redactedEntry) Trace() map[string]any    { return redactStringMap(e.Entry.Trace()) }
func (e redactedEntry) Owner() any               { return redactValue(e.Entry.Owner()) }
func (e redactedEntry) User() any                { return redactValue(e.Entry.User()) }

func (e redactedEntry) Tags() []string {
	src := e.Entry.Tags()
	if len(src) == 0 {
		return src
	}
	out := make([]string, len(src))
	for i, tag := range src {
		out[i] = RedactText(tag)
	}
	return out
}

func redactStringMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return src
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = redactField(k, v)
	}
	return out
}

func redactField(key string, v any) any {
	if sensitiveName(key) {
		return redactedMark
	}
	return redactValue(v)
}

func redactValue(v any) any {
	switch typed := v.(type) {
	case string:
		return RedactText(typed)
	case error:
		return RedactError(typed)
	case map[string]any:
		return redactStringMap(typed)
	default:
		return v
	}
}
