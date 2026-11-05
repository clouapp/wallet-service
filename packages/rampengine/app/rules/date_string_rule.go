package rules

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/validation"
)

// DateStringRule accepts RFC3339 timestamps or YYYY-MM-DD dates.
type DateStringRule struct{}

func (r *DateStringRule) Signature() string { return "date_string" }

func (r *DateStringRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (r *DateStringRule) Message(_ context.Context) string {
	return "must be YYYY-MM-DD or RFC3339"
}
