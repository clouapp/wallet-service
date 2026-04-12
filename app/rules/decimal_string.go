package rules

import (
	"context"
	"strconv"

	"github.com/goravel/framework/contracts/validation"
)

type DecimalString struct{}

func (r *DecimalString) Signature() string {
	return "decimal_string"
}

func (r *DecimalString) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	s, ok := val.(string)
	if !ok || s == "" {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func (r *DecimalString) Message(_ context.Context) string {
	return "The :attribute must be a valid decimal number."
}
