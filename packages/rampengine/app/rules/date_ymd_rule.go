package rules

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/validation"
)

// DateYmdRule accepts strict YYYY-MM-DD dates.
type DateYmdRule struct{}

func (r *DateYmdRule) Signature() string { return "date_ymd" }

func (r *DateYmdRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (r *DateYmdRule) Message(_ context.Context) string {
	return "must be a valid YYYY-MM-DD date"
}
