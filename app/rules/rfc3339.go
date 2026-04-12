package rules

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/validation"
)

type RFC3339 struct{}

func (r *RFC3339) Signature() string {
	return "rfc3339"
}

func (r *RFC3339) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

func (r *RFC3339) Message(_ context.Context) string {
	return "The :attribute must be a valid RFC3339 timestamp."
}
